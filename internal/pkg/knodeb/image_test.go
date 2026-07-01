package knodeb

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

func TestKnoBuildsInThePinnedImage(t *testing.T) {
	repo := filepath.Join(testutil.ModuleRoot(t), "data")
	o, err := DefaultOptions(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(o.Image, "@sha256:") {
		t.Errorf("kno builds in %q, a tag that can move; it must name the pinned digest", o.Image)
	}
}

func TestKnoCompilesFromTheModuleRoot(t *testing.T) {
	mod := testutil.ModuleRoot(t)
	for _, repo := range []string{mod, filepath.Join(mod, "data")} {
		got, err := sourceRoot(repo)
		if err != nil || got != mod {
			t.Errorf("sourceRoot(%s) = %q, %v; want the module root %s", repo, got, err, mod)
		}
	}
	if _, err := sourceRoot(t.TempDir()); err == nil {
		t.Error("a tree with no go.mod was accepted as kno's source")
	}
}
