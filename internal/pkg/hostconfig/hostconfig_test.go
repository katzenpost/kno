package hostconfig

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

func TestPackageWritesDebAndCleansUp(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	repo := testutil.RepoRoot(t)
	tmp := t.TempDir()

	stub := filepath.Join(tmp, "deb-build.sh")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\n: > \"$2\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEB_BUILD", stub)

	outdir := filepath.Join(tmp, "out")
	deb, err := Package(t.Context(), repo, "namenlos", "0.0.104", outdir)
	if err != nil {
		t.Fatalf("Package: %v", err)
	}
	if want := filepath.Join(outdir, "kno-host-config_0.0.104_all.deb"); deb != want {
		t.Errorf("deb path = %s, want %s", deb, want)
	}
	if _, err := os.Stat(deb); err != nil {
		t.Errorf("deb not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outdir, ".pkgbuild")); !os.IsNotExist(err) {
		t.Error("staging dir survived a successful build")
	}
}

func TestHostConfigDependsOnWhatDebconfNeeds(t *testing.T) {
	repo := testutil.RepoRoot(t)
	root, err := AssembleRoot(repo, "namenlos", "0.0.104", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "DEBIAN", "control"))
	if err != nil {
		t.Fatal(err)
	}
	ctl := string(b)
	for _, want := range []string{"debconf", "apt-utils"} {
		if !strings.Contains(ctl, want) {
			t.Errorf("control does not depend on %s:\n%s", want, ctl)
		}
	}
}
