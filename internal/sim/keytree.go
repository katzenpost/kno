package sim

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
)

func KeyTree(repo, network, dst string) (int, error) {
	src := filepath.Join(knopath.EditableDir(repo, network), "keys")
	ents, err := os.ReadDir(src)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return 0, err
	}
	n := 0
	for _, group := range ents {
		if !group.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(src, group.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || filepath.Ext(f.Name()) != ".pem" {
				continue
			}

			pem := filepath.Join(src, group.Name(), f.Name())
			if fsutil.IsSecretKeyFile(pem) {
				continue
			}
			b, err := os.ReadFile(pem)
			if err != nil {
				continue
			}
			if err := os.WriteFile(filepath.Join(dst, f.Name()), b, 0o644); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

func keyDirs(config string) []string {
	b, err := os.ReadFile(config)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, ln := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(ln, "=")
		if !ok || !isDirKey(strings.TrimSpace(k)) {
			continue
		}
		d := strings.Trim(strings.TrimSpace(v), `"`)

		if !filepath.IsAbs(d) || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

func isDirKey(k string) bool {
	switch k {
	case "DataDir", "StateDir", "KeyDir":
		return true
	}
	return false
}
