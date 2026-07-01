package deb

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheImageIsPulledOnceBeforeAnyPackageIsBuilt(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	fake := filepath.Join(dir, "podman")
	script := "#!/bin/sh\necho \"$1\" >> " + log + "\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PODMAN", fake)
	t.Setenv("DEB_BUILD", "")
	if err := prepareImage(context.Background(), EnvSettings()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(b)); got != "pull" {
		t.Errorf("podman calls %q, want one pull", got)
	}

	t.Setenv("DEB_BUILD", "/bin/true")
	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	if err := prepareImage(context.Background(), EnvSettings()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(log); err == nil {
		t.Error("a DEB_BUILD hook runs no container, but the image was pulled")
	}
}
