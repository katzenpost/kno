package build

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/source"
	"github.com/katzenpost/kno/internal/testutil"
)

func TestTheCrossImageCompilesCgoForEachArch(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	if _, err := exec.LookPath(compile.Podman()); err != nil {
		t.Skip("podman not available")
	}
	repo := testutil.RepoRoot(t)
	pins, err := source.ParsePins(filepath.Join(repo, "thirdparty", "pins.env"))
	if err != nil {
		t.Fatal(err)
	}
	u := upstreamDeb{Repo: repo, Work: t.TempDir(), Arch: "arm64",
		OS: compile.DefaultOS, Pins: pins, Cross: true}
	image, err := u.image(context.Background())
	if err != nil {
		t.Fatalf("cross image: %v", err)
	}
	tmp := t.TempDir()
	src := "package main\n\n// #include <stdlib.h>\nimport \"C\"\n\n" +
		"func main() { C.free(C.malloc(1)) }\n"
	main := filepath.Join(tmp, "main.go")
	if err := os.WriteFile(main, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	mod := []byte("module crosstest\n\ngo 1.21\n")
	gomod := filepath.Join(tmp, "go.mod")
	if err := os.WriteFile(gomod, mod, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, arch := range []string{"arm64", "ppc64el"} {
		goarch, cc, err := compile.ArchToGo(arch)
		if err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(compile.Podman(), "run", "--rm",
			"-v", tmp+":/src:rw", "-w", "/src",
			"-e", "CGO_ENABLED=1", "-e", "GOARCH="+goarch, "-e", "CC="+cc,
			"-e", "GOFLAGS=-buildvcs=false",
			image, "go", "build", "-o", "/dev/null", ".").CombinedOutput()
		if err != nil {
			t.Errorf("cgo cross build for %s (CC=%s): %v\n%s", arch, cc, err, out)
		}
	}
}
