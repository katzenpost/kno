package knodeb

import (
	"os"
	"path/filepath"
	"strings"
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

func TestCompileMountsTheModuleForAnArm64Package(t *testing.T) {
	root := testutil.ModuleRoot(t)
	tmp := t.TempDir()
	capture := filepath.Join(tmp, "args")
	engine := filepath.Join(tmp, "podman")
	write(t, engine, "#!/bin/sh\nprintf '%s\\n' \"$@\" > \""+capture+"\"\n", 0o755)
	o := Options{Repo: filepath.Join(root, "data"), Arch: "arm64", Image: "test:image", Podman: engine}
	if err := Compile(t.Context(), o, tmp); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{root + ":/repo:ro", "GOARCH=arm64", "cd /repo && go build"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("build invocation is missing %q:\n%s", want, b)
		}
	}
}
