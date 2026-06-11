package release

import (
	"fmt"
	"github.com/katzenpost/kno/internal/fsutil"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const StagedSumsName = "SHA256SUMS"

func stagedSkip(rel string) bool {
	return rel == StagedSumsName || rel == "pkgs" ||
		strings.HasPrefix(rel, "pkgs"+string(filepath.Separator))
}

func WriteStagedSums(relDir string) error {
	ents, err := walkStaged(relDir)
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, e := range ents {
		fmt.Fprintf(&b, "%s  %s\n", e.sum, e.rel)
	}
	return os.WriteFile(filepath.Join(relDir, StagedSumsName), []byte(b.String()), 0o644)
}

type stagedEntry struct{ rel, sum string }

func walkStaged(relDir string) ([]stagedEntry, error) {
	var out []stagedEntry
	err := filepath.WalkDir(relDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		r, rerr := filepath.Rel(relDir, p)
		if rerr != nil {
			return rerr
		}
		if r == "." {
			return nil
		}
		if stagedSkip(r) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}

		if !d.Type().IsRegular() {
			return nil
		}
		sum, _, serr := fsutil.SHA256File(p)
		if serr != nil {
			return serr
		}
		out = append(out, stagedEntry{rel: r, sum: sum})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out, nil
}

func StagedSums(relDir string) (map[string]string, bool) {
	b, err := os.ReadFile(filepath.Join(relDir, StagedSumsName))
	if err != nil {
		return nil, false
	}
	out := map[string]string{}
	for _, ln := range strings.Split(string(b), "\n") {
		sum, name, ok := strings.Cut(strings.TrimSpace(ln), "  ")
		if !ok || sum == "" || name == "" {
			continue
		}
		out[name] = sum
	}
	return out, true
}

func VerifyStaged(w io.Writer, relDir string, paths []string) error {
	want, ok := StagedSums(relDir)
	if !ok {
		if w != nil && len(paths) > 0 {
			fmt.Fprintf(w, "kno: this release staged no %s, so the %d file(s)"+
				" about to be copied were not checked against anything\n",
				StagedSumsName, len(paths))
		}
		return nil
	}
	var bad []string
	for _, p := range paths {
		r, err := filepath.Rel(relDir, p)
		if err != nil {
			return err
		}
		fi, err := os.Stat(p)
		if err != nil {
			bad = append(bad, fmt.Sprintf("%s: %v", r, err))
			continue
		}
		if fi.IsDir() {
			if err := verifyStagedDir(relDir, p, want, &bad); err != nil {
				return err
			}
			continue
		}
		verifyStagedOne(r, p, want, &bad)
	}
	if len(bad) > 0 {
		return fmt.Errorf("the staged release does not match its own %s:\n  %s\n"+
			"  Refusing to copy any of it. Nothing has been stopped. Push the\n"+
			"  release again; a transfer that was interrupted looks like this.",
			StagedSumsName, strings.Join(bad, "\n  "))
	}
	return nil
}

func verifyStagedOne(rel, p string, want map[string]string, bad *[]string) {
	sum, ok := want[rel]
	if !ok {
		*bad = append(*bad, rel+": not named in "+StagedSumsName)
		return
	}
	got, _, err := fsutil.SHA256File(p)
	if err != nil {
		*bad = append(*bad, fmt.Sprintf("%s: %v", rel, err))
		return
	}
	if got != sum {
		*bad = append(*bad, fmt.Sprintf("%s: staged %s, recorded %s", rel, got, sum))
	}
}

func verifyStagedDir(relDir, dir string, want map[string]string, bad *[]string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return err
		}
		r, rerr := filepath.Rel(relDir, p)
		if rerr != nil {
			return rerr
		}
		verifyStagedOne(r, p, want, bad)
		return nil
	})
}
