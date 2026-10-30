package knodeb

import (
	"os"
	"path/filepath"
	"runtime"
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
	if o.Binary != "" {
		t.Fatal("a source checkout reused the installed binary instead of compiling")
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

func TestNativePackageFromEmbeddedDataReusesTheInstalledBinary(t *testing.T) {
	repo := t.TempDir()
	pins, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "thirdparty", "pins.env"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "thirdparty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "thirdparty", "pins.env"), pins, 0o644); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "installed-kno")
	const payload = "installed kno bytes"
	if err := os.WriteFile(binary, []byte(payload), 0o755); err != nil {
		t.Fatal(err)
	}
	const version = "0.0.107-1+20261030194417.g5c64dae"
	o, err := defaultOptions(repo, version, binary)
	if err != nil {
		t.Fatal(err)
	}
	if o.Binary != binary || o.Src != "" || o.Version != version {
		t.Fatalf("embedded build did not preserve the installed binary identity: %+v", o)
	}
	o.OS, o.OutDir = runtime.GOOS, t.TempDir()
	root, err := AssembleRoot(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "usr/bin/kno"))
	if err != nil || string(got) != payload {
		t.Fatalf("packaged binary differs from the installed binary: %q, %v", got, err)
	}
	control, err := os.ReadFile(filepath.Join(root, "DEBIAN/control"))
	if err != nil || !strings.Contains(string(control), "Version: "+version+"\n") {
		t.Fatalf("installed binary was relabelled: %s, %v", control, err)
	}
	out := filepath.Join(t.TempDir(), "native-kno")
	if err := CompileLocal(t.Context(), o, "unused", out); err != nil {
		t.Fatalf("native package still requires Go source: %v", err)
	}
	if _, err := defaultOptions(repo, "(devel)", binary); err == nil {
		t.Fatal("an unversioned binary acquired a fabricated package version")
	}
}

func TestInstalledBinaryCannotBecomeACrossCompiledPackage(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "installed-kno")
	if err := os.WriteFile(binary, []byte("native binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	arch := "arm64"
	if runtime.GOARCH == arch {
		arch = "amd64"
	}
	o := Options{Binary: binary, Arch: arch, OS: runtime.GOOS}
	out := filepath.Join(t.TempDir(), "foreign-kno")
	if err := CompileLocal(t.Context(), o, "unused", out); err == nil || !strings.Contains(err.Error(), "source checkout") {
		t.Fatalf("cross build reused a native executable: %v", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("a foreign package binary was written: %v", err)
	}
}
