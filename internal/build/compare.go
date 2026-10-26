package build

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/pkg/deb"
)

type Difference struct {
	Package string

	Kind string
	Want string
	Got  string
}

func (d Difference) String() string {
	switch d.Kind {
	case "missing":
		return fmt.Sprintf("%s: in the reference, not in this build", d.Package)
	case "unexpected":
		return fmt.Sprintf("%s: in this build, not in the reference", d.Package)
	case "absent":
		return fmt.Sprintf("%s: not in this build, and %s needs it", d.Package, d.Want)
	}
	return fmt.Sprintf("%s: %s is %s, the reference says %s", d.Package, d.Kind, d.Got, d.Want)
}

func Compare(ref, got PackageSums) []Difference {
	var out []Difference
	refByName := map[string]PackageSum{}
	for _, p := range ref.Packages {
		refByName[p.Name] = p
	}
	gotByName := map[string]PackageSum{}
	for _, p := range got.Packages {
		gotByName[p.Name] = p
	}

	names := make([]string, 0, len(refByName)+len(gotByName))
	for n := range refByName {
		names = append(names, n)
	}
	for n := range gotByName {
		if _, ok := refByName[n]; !ok {
			names = append(names, n)
		}
	}
	sort.Strings(names)

	for _, n := range names {
		r, inRef := refByName[n]
		g, inGot := gotByName[n]
		switch {
		case !inGot:
			out = append(out, Difference{Package: n, Kind: "missing"})
			continue
		case !inRef:
			out = append(out, Difference{Package: n, Kind: "unexpected"})
			continue
		}
		if r.Sha256 != g.Sha256 {
			out = append(out, Difference{Package: n, Kind: "digest", Want: r.Sha256, Got: g.Sha256})
		}
		if r.Version != "" && g.Version != "" && r.Version != g.Version {
			out = append(out, Difference{Package: n, Kind: "version", Want: r.Version, Got: g.Version})
		}
		rs, gs := strings.Join(r.Scripts, ","), strings.Join(g.Scripts, ",")
		if rs != gs {
			out = append(out, Difference{
				Package: n, Kind: "scripts",
				Want: orNone(rs), Got: orNone(gs),
			})
		}
	}
	return out
}

func Uncovered(s PackageSums) []Difference {
	have := map[string]bool{}
	need := map[string]string{}
	want := func(name, by string) {
		if need[name] == "" {
			need[name] = by
		}
	}
	for _, p := range s.Packages {
		base := path.Base(p.Name)
		if name, arch, ok := deb.SplitName(base); ok {
			have[name+"_*_"+arch+".deb"] = true
			if arch != deb.ArchAll && nodePackage(name) {
				want("katzenpost_*_"+arch+".deb", base)
				want("kno_*_"+arch+".deb", base)
			}
			continue
		}
		switch {
		case !strings.HasSuffix(base, ".pkg"):
		case nodePackage(base):
			want("katzenpost-*.pkg", base)
			want("kno-*.pkg", base)
		case strings.HasPrefix(base, "katzenpost-"):
			have["katzenpost-*.pkg"] = true
		case strings.HasPrefix(base, "kno-"):
			have["kno-*.pkg"] = true
		}
	}
	names := make([]string, 0, len(need))
	for n := range need {
		if !have[n] {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	out := make([]Difference, 0, len(names))
	for _, n := range names {
		out = append(out, Difference{Package: n, Kind: "absent", Want: need[n]})
	}
	return out
}

func nodePackage(name string) bool {
	return strings.HasPrefix(name, knopath.ConfigPkgPrefix) || strings.HasPrefix(name, "kno-host-config")
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func LoadManifest(path string) (PackageSums, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return PackageSums{}, err
	}
	var m PackageSums
	if err := json.Unmarshal(b, &m); err != nil {
		return PackageSums{}, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

func DirSums(dir string) (PackageSums, error) {
	s := PackageSums{Dir: dir}
	if m, err := LoadManifest(filepath.Join(dir, ManifestName)); err == nil {
		s.Network, s.Tag, s.Inputs = m.Network, m.Tag, m.Inputs
	}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		sum, n, err := fsutil.SHA256File(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		s.Packages = append(s.Packages, PackageSum{Name: filepath.ToSlash(rel), Sha256: sum, Bytes: n})
		return nil
	})
	return s, err
}

func LoadBuild(p string) (PackageSums, error) {
	if fsutil.IsDir(p) {
		return DirSums(p)
	}
	m, err := LoadManifest(p)
	if err != nil {
		return m, err
	}
	for _, d := range nodeRecordDirs(filepath.Dir(p)) {
		if n, err := LoadManifest(filepath.Join(d, ManifestName)); err == nil {
			m.Packages = append(m.Packages, n.Packages...)
		}
	}
	sort.Slice(m.Packages, func(i, j int) bool { return m.Packages[i].Name < m.Packages[j].Name })
	return m, nil
}

func ReportCompare(w io.Writer, ref, got PackageSums, diffs []Difference) int {
	if ref.Tag != "" && got.Tag != "" && ref.Tag != got.Tag {
		fmt.Fprintf(w, "kno build: the reference is for %s and this build is %s."+
			" A comparison of them proves nothing\n", ref.Tag, got.Tag)
		return 1
	}
	if len(ref.Packages) == 0 {
		fmt.Fprintln(w, "kno build: the reference names no package. A comparison with it proves nothing")
		return 1
	}
	if len(diffs) == 0 {
		if ref.Dir != "" {
			fmt.Fprintf(w, "kno build: %d file(s) in %s match %s\n",
				len(ref.Packages), got.Dir, ref.Dir)
		} else {
			fmt.Fprintf(w, "kno build: %d package(s) match the manifest for %s\n",
				len(ref.Packages), ref.Tag)
		}
		if ref.Inputs != nil && got.Inputs != nil && ref.Inputs.Tool != got.Inputs.Tool {

			fmt.Fprintf(w, "  built by kno %s, the reference by kno %s\n",
				got.Inputs.Tool, ref.Inputs.Tool)
		}
		return 0
	}
	fmt.Fprintf(w, "kno build: %d difference(s) from the reference for %s:\n", len(diffs), ref.Tag)
	for _, d := range diffs {
		fmt.Fprintf(w, "  %s\n", d)
	}
	fmt.Fprintln(w, "kno build: this build did not reproduce the release.")
	fmt.Fprintln(w, "  A digest that cannot be reproduced is a failed release, not a new one.")
	if ref.Inputs != nil {
		fmt.Fprintf(w, "  The reference was built from katzenpost %s (%s) by kno %s.\n",
			ref.Inputs.KatzenpostRef, shortSha(ref.Inputs.KatzenpostSha), ref.Inputs.Tool)
	}
	return 1
}

func shortSha(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
