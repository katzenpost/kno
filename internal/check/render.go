package check

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"

	"github.com/katzenpost/kno/internal/render"
)

type RenderOptions struct {
	Repo string

	Networks []string

	Reference string

	ReferenceNetwork string
}

func Render(o RenderOptions) (Result, error) {
	var r Result
	if o.Repo == "" {
		return r, fmt.Errorf("render check: no repo given")
	}
	nets := o.Networks
	if len(nets) == 0 {
		var err error
		nets, err = networksWithManifest(o.Repo)
		if err != nil {
			return r, err
		}
	}
	if len(nets) == 0 {
		return r, fmt.Errorf("render check: no networks/<name>/editable-configs/manifest.toml under %s", o.Repo)
	}

	for _, name := range nets {
		netDir := knopath.NetworkDir(o.Repo, name)
		c, err := renderAndCompare(netDir)
		if err != nil {
			return r, err
		}
		report(&r, "render", "render-drift "+name, c)
	}

	refNet := o.ReferenceNetwork
	if refNet == "" {
		refNet = "namenlos"
	}
	ref := o.Reference
	if ref == "" {
		ref = filepath.Join(o.Repo, "reference", refNet, "configs")
	}
	subject := "parity " + refNet + " vs reference"
	if _, err := os.Stat(ref); err != nil {
		r.add("render", subject, Skip, "no reference configs at %s. Parity against the upstream reference not checked", ref)
		return r, nil
	}
	c, err := compareDirs(knopath.ConfigsDir(o.Repo, refNet), ref)
	if err != nil {
		return r, err
	}
	report(&r, "render", subject, c)
	return r, nil
}

func report(r *Result, check, subject string, c comparison) {
	if len(c.Diffs) == 0 {
		r.add(check, subject, Pass, "%d configs match", c.Matched)
		return
	}
	r.add(check, subject, Fail, "%d difference(s): %s", len(c.Diffs), strings.Join(c.Diffs, "; "))
}

func renderAndCompare(netDir string) (comparison, error) {
	out, err := os.MkdirTemp("", "kno-render-check-")
	if err != nil {
		return comparison{}, err
	}
	defer func() { _ = os.RemoveAll(out) }()

	var rerr error
	if _, err := capture("", func() { rerr = render.Generate(netDir, out) }); err != nil {
		return comparison{}, err
	}
	if rerr != nil {
		return comparison{}, rerr
	}
	gen := knopath.GeneratedIn(netDir)
	var c comparison
	for _, rel := range []string{"configs", knopath.ServicesName, knopath.KeysName, knopath.NetworkFileName} {
		if err := compareBytes(&c, filepath.Join(gen, rel), filepath.Join(out, rel)); err != nil {
			return c, err
		}
	}
	return c, nil
}

func compareBytes(c *comparison, committed, fresh string) error {
	have, err := fileSums(committed)
	if err != nil {
		return err
	}
	want, err := fileSums(fresh)
	if err != nil {
		return err
	}
	for rel, sum := range want {
		switch got, ok := have[rel]; {
		case !ok:
			c.Diffs = append(c.Diffs, "MISSING in "+committed+": "+rel)
		case got != sum:
			c.Diffs = append(c.Diffs, "DIFF: "+filepath.Join(filepath.Base(committed), rel))
		default:
			c.Matched++
		}
	}
	for rel := range have {
		if _, ok := want[rel]; !ok {
			c.Diffs = append(c.Diffs, "EXTRA in "+committed+": "+rel)
		}
	}
	sort.Strings(c.Diffs)
	return nil
}

func fileSums(root string) (map[string]string, error) {
	out := map[string]string{}
	fi, err := os.Stat(root)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		sum, _, err := fsutil.SHA256File(root)
		if err != nil {
			return nil, err
		}
		out["."] = sum
		return out, nil
	}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		sum, _, err := fsutil.SHA256File(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out[rel] = sum
		return nil
	})
	return out, err
}

func networksWithManifest(repo string) ([]string, error) {
	ents, err := os.ReadDir(knopath.NetworksDir(repo))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(knopath.EditableDir(repo, e.Name()), "manifest.toml")); err == nil {
			out = append(out, e.Name())
		}
	}
	return out, nil
}
