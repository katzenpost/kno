package service

import (
	"os"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/knopath"

	"github.com/katzenpost/kno/internal/testutil"
)

func services(t *testing.T, repo, network string) []string {
	t.Helper()
	gc := knopath.ConfigsDir(repo, network)
	entries, err := os.ReadDir(gc)
	if err != nil {
		t.Fatalf("read .generated/configs: %v", err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".toml") || !strings.Contains(name, "-pq-") {
			continue
		}
		out = append(out, strings.TrimSuffix(name, ".toml"))
	}
	return out
}

func TestManifestFragmentIsComplete(t *testing.T) {
	repo := testutil.RepoRoot(t)
	svcs := services(t, repo, "namenlos")
	if len(svcs) == 0 {
		t.Fatal("no services to check")
	}
	for _, svc := range svcs {
		s, err := ResolveService(repo, "namenlos", svc)
		if err != nil {
			t.Fatalf("ResolveService %s: %v", svc, err)
		}
		frag := s.ManifestFragment()
		for _, key := range []string{
			"BUILD=", "BINARY_SRC=", "BINARY_DST=", "UNIT=",
			"CONFIG_DST=", "STATE_DIR=", "SERVICE_USER=",
		} {
			if !strings.Contains(frag, key) {
				t.Errorf("%s: manifest fragment has no %s:\n%s", svc, key, frag)
			}
		}

		for _, kv := range [][2]string{
			{"BINARY_DST", s.BinaryDst}, {"UNIT", s.Unit},
			{"CONFIG_DST", s.ConfigDst}, {"STATE_DIR", s.StateDir},
		} {
			if !strings.Contains(frag, kv[0]+`="`+kv[1]+`"`) && kv[1] != "" {
				t.Errorf("%s: %s=%q is not in the fragment:\n%s", svc, kv[0], kv[1], frag)
			}
		}
	}
}
