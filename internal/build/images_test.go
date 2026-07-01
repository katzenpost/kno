package build

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

func TestPrepareImagesBuildsTheImagesBuildUses(t *testing.T) {
	dir := t.TempDir()
	logf := filepath.Join(dir, "argv")
	fake := filepath.Join(dir, "podman")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho \"$*\" >> "+logf+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PODMAN", fake)
	o := Options{Repo: testutil.RepoRoot(t), Network: "namenlos",
		Services: []string{"mixy-pq-mixserver"}, Arches: []string{"amd64"}, WorkDir: dir}
	if err := o.PrepareImages(io.Discard); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(logf)
	if err != nil {
		t.Fatalf("PrepareImages ran no podman command: %v", err)
	}
	got := string(b)
	if !strings.Contains(got, "build ") || !strings.Contains(got, "-t kno-katzenpost-deb-ubuntu25.04-amd64") {
		t.Errorf("PrepareImages did not build the katzenpost deb image for mixy:\n%s", got)
	}
	if strings.Contains(got, "kno-build-") {
		t.Errorf("PrepareImages built an image that Build no longer uses:\n%s", got)
	}
}
