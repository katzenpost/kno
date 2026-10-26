package preflight

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/fsutil"

	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/pkg/deb"
)

type LocalState struct {
	Dir      string
	Current  string
	LKG      string
	Pending  string
	Releases []string

	Quarantined []string

	Exists bool
}

func ReadLocalState(stateDir, releasesDir string) LocalState {
	s := LocalState{Dir: stateDir}
	if fi, err := os.Stat(stateDir); err != nil || !fi.IsDir() {
		return s
	}
	s.Exists = true
	s.Current = fsutil.FirstLine(filepath.Join(stateDir, "current"))

	s.LKG = fsutil.FirstLine(filepath.Join(stateDir, "last-known-good"))
	if st := fsutil.FirstLine(filepath.Join(stateDir, "state")); st == "pending" {
		kv := readKV(filepath.Join(stateDir, "pending"))
		s.Pending = kv["tag"]
		if s.Pending == "" {
			s.Pending = "unknown"
		}
	}
	for _, ln := range lines(filepath.Join(stateDir, "quarantine")) {
		if ln != "" {
			s.Quarantined = append(s.Quarantined, ln)
		}
	}
	if ents, err := os.ReadDir(releasesDir); err == nil {
		for _, e := range ents {
			if e.IsDir() {
				s.Releases = append(s.Releases, e.Name())
			}
		}
	}
	return s
}

func (s LocalState) Usable(releasesDir, tag string) bool {
	if tag == "" {
		return false
	}
	fi, err := os.Stat(filepath.Join(releasesDir, tag, "manifest"))
	return err == nil && fi.IsDir()
}

func lines(p string) []string {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var out []string
	for _, ln := range strings.Split(string(b), "\n") {
		out = append(out, strings.TrimSpace(ln))
	}
	return out
}

func readKV(p string) map[string]string {
	m := map[string]string{}
	for _, ln := range lines(p) {
		if k, v, ok := strings.Cut(ln, "="); ok {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m
}

func ReleasePackages(releasesDir, tag string) map[string][]string {
	hits, err := filepath.Glob(filepath.Join(releasesDir, tag, "pkgs", "*.deb"))
	if err != nil {
		return nil
	}
	out := map[string][]string{}
	for _, p := range hits {
		name, version, ok := splitDebName(filepath.Base(p))
		if !ok {
			continue
		}
		if !slices.Contains(out[name], version) {
			out[name] = append(out[name], version)
		}
	}
	for _, v := range out {
		sort.Strings(v)
	}
	return out
}

func splitDebName(base string) (name, version string, ok bool) {
	trimmed := strings.TrimSuffix(base, ".deb")
	if trimmed == base {
		return "", "", false
	}
	parts := strings.Split(trimmed, "_")
	if len(parts) < 3 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func Candidate(installed string, candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}
	want := osTokenOf(installed)
	var best string
	for _, c := range candidates {
		if osTokenOf(c) == want {
			if best == "" || deb.CompareVersions(best, c) < 0 {
				best = c
			}
		}
	}
	if best != "" {
		return best
	}
	for _, c := range candidates {
		if best == "" || deb.CompareVersions(best, c) < 0 {
			best = c
		}
	}
	return best
}

func osTokenOf(version string) string {
	for _, part := range strings.Split(version, "+") {
		if compile.IsOSToken(part) {
			return part
		}
	}
	return ""
}
