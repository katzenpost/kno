package compile

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

func TestCrossToolchainCompilesCgo(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	if _, err := exec.LookPath(Podman()); err != nil {
		t.Skip("podman not available")
	}
	repo := testutil.RepoRoot(t)
	image, err := EnsureImage(repo, DefaultOS)
	if err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}

	tmp := t.TempDir()
	src := `package main

// #include <stdlib.h>
// #include <stdio.h>
import "C"

func main() { C.free(C.malloc(1)) }
`
	if err := os.WriteFile(filepath.Join(tmp, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module crosstest\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, arch := range []string{"arm64", "ppc64el"} {
		goarch, cc, err := ArchToGo(arch)
		if err != nil {
			t.Fatalf("archToGo %s: %v", arch, err)
		}
		cmd := exec.Command(Podman(), "run", "--rm",
			"-v", tmp+":/src:rw", "-w", "/src",
			"-e", "CGO_ENABLED=1", "-e", "GOOS=linux",
			"-e", "GOARCH="+goarch, "-e", "CC="+cc,
			"-e", "GOFLAGS=-buildvcs=false",
			image, "go", "build", "-o", "/dev/null", ".")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("cross cgo build for %s (CC=%s) failed: %v\n%s", arch, cc, err, out)
			continue
		}
		t.Logf("cross cgo toolchain works for %s (CC=%s)", arch, cc)
	}
}

func TestDebArchNamesWhatDebianNames(t *testing.T) {
	for goarch, want := range map[string]string{"amd64": "amd64", "arm64": "arm64", "ppc64le": "ppc64el"} {
		if got := DebArch(goarch); got != want {
			t.Errorf("DebArch(%s) = %s, want %s", goarch, got, want)
		}
	}
}
