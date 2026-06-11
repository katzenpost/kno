package config

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/katzenpost/kno/internal/knopath"
)

func wholeTree(t *testing.T, dir string) {
	t.Helper()
	for _, p := range []string{
		knopath.NetworkFile(dir, "namenlos"),
		knopath.ConfigsDir(dir, "namenlos"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("the embedded tree at %s is not whole: %v", dir, err)
		}
	}
}

func TestEmbeddedRepoIsWholeWhenMaterializedConcurrently(t *testing.T) {
	t.Setenv("KNO_CACHE", t.TempDir())
	var wg sync.WaitGroup
	dirs := make([]string, 8)
	errs := make([]error, 8)
	for i := range dirs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			dirs[i], errs[i] = EmbeddedRepo()
		}(i)
	}
	wg.Wait()
	for i := range dirs {
		if errs[i] != nil {
			t.Fatalf("EmbeddedRepo: %v", errs[i])
		}
		wholeTree(t, dirs[i])
	}
}

func TestEmbeddedRepoReplacesAPartialTree(t *testing.T) {
	t.Setenv("KNO_CACHE", t.TempDir())
	dir, err := EmbeddedRepo()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "networks")); err != nil {
		t.Fatal(err)
	}
	again, err := EmbeddedRepo()
	if err != nil {
		t.Fatalf("EmbeddedRepo over a partial tree: %v", err)
	}
	wholeTree(t, again)
}
