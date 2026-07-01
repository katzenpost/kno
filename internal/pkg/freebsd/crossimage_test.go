package freebsd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTheCrossImageIsBuiltForThisMachinesArchitecture(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, "packaging", "freebsd")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Containerfile.cross"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(t.TempDir(), "args")
	fake := filepath.Join(t.TempDir(), "podman")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho \"$@\" >"+argsFile+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PODMAN", fake)

	image, err := CrossImage(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(image, "-"+runtime.GOARCH) {
		t.Errorf("tag %s does not name %s, so two architectures share one tag", image, runtime.GOARCH)
	}
	b, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "--platform linux/"+runtime.GOARCH+" ") {
		t.Errorf("the image build picks whatever variant the local store resolves: %s", b)
	}
}
