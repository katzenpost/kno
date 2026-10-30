package track

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/katzenpost/kno/internal/fsutil"
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
	old := fsutil.Euid
	fsutil.Euid = func() int { return 0 }
	t.Cleanup(func() { fsutil.Euid = old })
}

func notRoots(t *testing.T, dir string) {
	t.Helper()
	if os.Geteuid() != 0 {
		return
	}
	chown := func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(p, 1000, 1000)
	}
	if err := filepath.WalkDir(dir, chown); err != nil {
		t.Fatal(err)
	}
}

func TestTheCallerCannotChooseTheRepoRootReads(t *testing.T) {
	mine := fakeCheckout(t)
	notRoots(t, mine)
	t.Setenv("KNO_REPO", mine)
	asRoot(t)
	if got := repoDir(t.TempDir()); got == mine {
		t.Errorf("root takes the repo of its caller: %s", got)
	}
}

func TestACheckoutRootDoesNotOwnIsNotRoots(t *testing.T) {
	fake := fakeCheckout(t)
	notRoots(t, fake)
	t.Setenv("KNO_REPO", "")
	t.Chdir(fake)
	asRoot(t)
	if got := repoDir(t.TempDir()); got == fake {
		t.Errorf("root takes the checkout in its working directory: %s", got)
	}
}

func TestKnoUpdateUsesTheTrustedCheckoutInTheWorkingDirectory(t *testing.T) {
	fake := fakeCheckout(t)
	t.Setenv("KNO_REPO", "")
	t.Chdir(fake)
	if got := repoDir(t.TempDir()); got != fake {
		t.Errorf("repoDir ignored the trusted checkout: got %s, want %s", got, fake)
	}
}
