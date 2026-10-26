package track

import (
	"os"
	"path/filepath"
	"testing"
)

func fakeCheckout(t *testing.T) string {
	t.Helper()
	fake := t.TempDir()
	svcs := filepath.Join(fake, "networks", "namenlos", ".generated", "services")
	if err := os.MkdirAll(svcs, 0o755); err != nil {
		t.Fatal(err)
	}
	conf := "BACKEND=\"systemd\"\nSTATE_DIR=\"/etc\"\nCONFIG_DST=\"/etc/sudoers\"\n"
	if err := os.WriteFile(filepath.Join(svcs, "mixy-pq-mixserver.conf"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	return fake
}

func asRoot(t *testing.T) {
	t.Helper()
	old := euid
	euid = func() int { return 0 }
	t.Cleanup(func() { euid = old })
}

func TestTheCallerCannotChooseTheRepoRootReads(t *testing.T) {
	mine := fakeCheckout(t)
	t.Setenv("KNO_REPO", mine)
	asRoot(t)
	if got := repoDir(t.TempDir()); got == mine {
		t.Errorf("root takes the repo of its caller: %s", got)
	}
}

func TestARepoInTheWorkingDirectoryIsNotRoots(t *testing.T) {
	fake := fakeCheckout(t)
	t.Setenv("KNO_REPO", "")
	t.Chdir(fake)
	asRoot(t)
	if got := repoDir(t.TempDir()); got == fake {
		t.Errorf("root takes the checkout in its working directory: %s", got)
	}
}
