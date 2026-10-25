package build

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/release"
)

type PackageSums struct {
	Network string    `json:"network"`
	Tag     string    `json:"tag"`
	Written time.Time `json:"written"`

	Revision string `json:"revision,omitempty"`

	SourceDateEpoch string `json:"source_date_epoch,omitempty"`

	ToolRevision string `json:"tool_revision,omitempty"`

	Build string `json:"build,omitempty"`

	Inputs *BuildInputs `json:"inputs,omitempty"`

	Simulation json.RawMessage `json:"simulation,omitempty"`

	Packages []PackageSum `json:"packages"`

	Dir string `json:"-"`
}

type BuildInputs struct {
	KatzenpostRef string `json:"katzenpost_ref,omitempty"`
	KatzenpostSha string `json:"katzenpost_sha,omitempty"`
	HpqcRef       string `json:"hpqc_ref,omitempty"`
	HpqcSha       string `json:"hpqc_sha,omitempty"`
	GoVersion     string `json:"go_version,omitempty"`

	BuildImage string `json:"build_image,omitempty"`
	DebImage   string `json:"deb_image,omitempty"`

	Tool string `json:"tool,omitempty"`
}

type PackageSum struct {
	Name string `json:"name"`

	Arch    string `json:"arch,omitempty"`
	Version string `json:"version,omitempty"`
	Sha256  string `json:"sha256"`
	Bytes   int64  `json:"bytes"`

	Content string `json:"content,omitempty"`

	Scripts []string `json:"scripts,omitempty"`
}

const (
	SumsName     = release.SumsName
	ManifestName = release.ManifestName

	SimulationName = "simulation.json"
)

func SumsWithContent(network, tag, revision, repo string, pkgs []string,
	content map[deb.PkgID]string, now time.Time) (PackageSums, error) {
	out := PackageSums{Network: network, Tag: tag, Written: now, Revision: revision,

		SourceDateEpoch: os.Getenv("SOURCE_DATE_EPOCH")}
	if repo != "" {
		out.Inputs = readInputs(repo)
	}
	out.Simulation = simulationFor(pkgs)
	for _, p := range pkgs {
		sum, n, err := fsutil.SHA256File(p)
		if err != nil {
			return out, err
		}
		arch, version, scripts := describe(p)
		base := filepath.Base(p)
		out.Packages = append(out.Packages, PackageSum{
			Name: base, Arch: arch, Version: version,
			Content: content[ID(base)],
			Sha256:  sum, Bytes: n, Scripts: scripts,
		})
	}
	sort.Slice(out.Packages, func(i, j int) bool {
		return out.Packages[i].Name < out.Packages[j].Name
	})
	return out, nil
}

func (s PackageSums) SumsFile() string {
	var b []byte
	for _, p := range s.Packages {
		b = append(b, fmt.Sprintf("%s  %s\n", p.Sha256, p.Name)...)
	}
	return string(b)
}

func WriteSums(dir string, s PackageSums) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.WriteFile(filepath.Join(dir, ManifestName), b, 0o644); err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	body := s.SumsFile() + fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), ManifestName)
	return os.WriteFile(filepath.Join(dir, SumsName), []byte(body), 0o644)
}

type SumsError struct {
	Name, Want, Got string
	Err             error
}

func (e SumsError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Name, e.Err)
	}
	return fmt.Sprintf("%s: recorded %s but the file on disk is %s", e.Name, e.Want, e.Got)
}

func VerifySums(dir string, s PackageSums) []SumsError {
	var bad []SumsError
	for _, p := range s.Packages {
		path := filepath.Join(dir, p.Name)
		got, n, err := fsutil.SHA256File(path)
		if err != nil {
			bad = append(bad, SumsError{Name: p.Name, Want: p.Sha256, Err: err})
			continue
		}
		if got != p.Sha256 || (p.Bytes > 0 && n != p.Bytes) {
			bad = append(bad, SumsError{Name: p.Name, Want: p.Sha256, Got: got})
		}
	}
	return bad
}

func recordAndVerify(o Options, pkgs []string) error {

	for _, dir := range pkgDirs(pkgs) {
		note("computing the SHA-256 of %d packages", len(dir.pkgs))
		sums, err := SumsWithContent(o.Network, o.Tag, o.Revision, o.Repo,
			dir.pkgs, o.Content, buildTime())
		if err != nil {
			return fmt.Errorf("hashing packages: %w", err)
		}
		sums.ToolRevision = o.toolRevision()
		if k := o.kind(); k != NativeBuild {
			sums.Build = k
		}
		sums = sums.Merge(Existing(dir.dir))
		if err := WriteSums(dir.dir, sums); err != nil {
			return fmt.Errorf("writing %s: %w", SumsName, err)
		}
		bad := VerifySums(dir.dir, sums)
		ReportVerify(os.Stderr, sums, bad, dir.dir, o.Verbose, basenames(dir.pkgs))
		if len(bad) > 0 {
			return fmt.Errorf("%d package(s) do not match %s", len(bad), SumsName)
		}
	}
	return nil
}

func basenames(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, filepath.Base(p))
	}
	return out
}

type pkgGroup struct {
	dir  string
	pkgs []string
}

func pkgDirs(pkgs []string) []pkgGroup {
	var out []pkgGroup
	at := map[string]int{}
	for _, p := range pkgs {
		d := filepath.Dir(p)
		i, ok := at[d]
		if !ok {
			at[d] = len(out)
			out = append(out, pkgGroup{dir: d})
			i = len(out) - 1
		}
		out[i].pkgs = append(out[i].pkgs, p)
	}
	return out
}

func ReportVerify(w io.Writer, s PackageSums, bad []SumsError, dir string, verbose bool, built []string) {
	if len(bad) > 0 {
		fmt.Fprintf(w, "kno build: %d of %d package(s) do NOT match %s:\n",
			len(bad), len(s.Packages), SumsName)
		for _, e := range bad {
			fmt.Fprintf(w, "  FAILED  %s\n", e)
		}
		fmt.Fprintf(w, "kno build: the packages changed after they were built."+
			" Do not install or ship them; build again.\n")
		return
	}
	mine := map[string]bool{}
	for _, b := range built {
		mine[b] = true
	}
	if verbose {

		var shown int
		for _, p := range s.Packages {
			if len(mine) > 0 && !mine[p.Name] {
				continue
			}
			shown++
			fmt.Fprintf(w, "  %s  %s: OK\n", p.Sha256, p.Name)
		}
		if n := len(s.Packages) - shown; n > 0 {
			fmt.Fprintf(w, "  (and %d package(s) already in this release, which match"+
				" and are not listed)\n", n)
		}
	}
	if verbose {
		fmt.Fprintf(w, "kno build: %d packages match %s\n", len(s.Packages), SumsName)
	}
	if verbose && dir != "" {

		fmt.Fprintf(w, "  elsewhere: cd %s && sha256sum -c %s\n", dir, SumsName)
	}
}

func buildTime() time.Time {
	if v := os.Getenv("SOURCE_DATE_EPOCH"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return time.Unix(n, 0).UTC()
		}
	}
	return time.Now().UTC()
}

func Existing(dir string) []PackageSum {
	return present(dir, recorded(dir))
}

func Recorded(dir string) []PackageSum { return recorded(dir) }

func present(dir string, in []PackageSum) []PackageSum {
	out := in[:0]
	for _, p := range in {
		if _, err := os.Stat(filepath.Join(dir, p.Name)); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func recorded(dir string) []PackageSum {
	out := recordedFlat(dir)
	for _, d := range nodeRecordDirs(dir) {
		out = append(out, recordedFlat(d)...)
	}
	return out
}

func recordedFlat(dir string) []PackageSum {
	b, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err == nil {
		var s PackageSums
		if json.Unmarshal(b, &s) == nil {
			return s.Packages
		}
	}
	b, err = os.ReadFile(filepath.Join(dir, SumsName))
	if err != nil {
		return nil
	}
	var out []PackageSum
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 {
			out = append(out, PackageSum{Sha256: f[0], Name: f[1]})
		}
	}
	return out
}

func (s PackageSums) Merge(old []PackageSum) PackageSums {
	have := map[string]bool{}
	for _, p := range s.Packages {
		have[p.Name] = true
	}
	for _, p := range old {
		if have[p.Name] || p.Name == "" {
			continue
		}
		s.Packages = append(s.Packages, p)
	}
	sort.Slice(s.Packages, func(i, j int) bool { return s.Packages[i].Name < s.Packages[j].Name })
	return s
}

func simulationFor(pkgs []string) json.RawMessage {
	if len(pkgs) == 0 {
		return nil
	}

	rel := filepath.Dir(filepath.Dir(pkgs[0]))
	b, err := os.ReadFile(filepath.Join(rel, SimulationName))
	if err != nil || !json.Valid(b) {
		return nil
	}
	return json.RawMessage(b)
}

func SumsEntries(path string) ([]PackageSum, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []PackageSum
	for _, line := range strings.Split(string(b), "\n") {
		if f := strings.Fields(line); len(f) == 2 {
			out = append(out, PackageSum{Sha256: f[0], Name: f[1]})
		}
	}
	return out, nil
}
