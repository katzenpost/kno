package knodeb

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

func TestTheKnoPackageCompilesFromTheModuleNotTheDataRoot(t *testing.T) {
	root := testutil.ModuleRoot(t)
	o, err := DefaultOptions(filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	if o.Src != root {
		t.Fatalf("kno compiles from %q, want the module root %q: the data root has no go.mod", o.Src, root)
	}
	if _, err := os.Stat(filepath.Join(o.Src, "go.mod")); err != nil {
		t.Fatal(err)
	}
	if o2, err := DefaultOptions(root); err == nil && o2.Src != root {
		t.Errorf("given the module root, kno compiles from %q", o2.Src)
	}
}

func TestNoModuleIsAnError(t *testing.T) {
	if _, err := sourceRoot(t.TempDir()); err == nil {
		t.Fatal("a directory with no go.mod above it gave a module root")
	}
}
