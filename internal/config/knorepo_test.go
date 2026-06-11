package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

func TestKnoRepoIsResolvedToTheDataRoot(t *testing.T) {
	root := testutil.ModuleRoot(t)
	resolves := map[string]bool{
		"internal/config/config.go":      true,
		"internal/track/env.go":          true,
		"internal/contribute/command.go": true,
	}
	err := filepath.Walk(filepath.Join(root, "internal"), func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), `Getenv("KNO_REPO")`) && !resolves[filepath.ToSlash(rel)] {
			t.Errorf("%s reads KNO_REPO itself; a checkout path then misses data/. Use config.RepoDir", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	d := t.TempDir()
	if err := os.MkdirAll(filepath.Join(d, "data", "networks"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KNO_REPO", d)
	if got := RepoDir(); got != filepath.Join(d, "data") {
		t.Errorf("RepoDir with KNO_REPO=%s is %s, want its data root", d, got)
	}
}
