package backupkeys

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/service"
)

func TestABackupDoesNotFollowALinkOutOfTheStateDir(t *testing.T) {
	state, dest := t.TempDir(), t.TempDir()
	outside := filepath.Join(t.TempDir(), "shadow")
	if err := os.WriteFile(outside, []byte("root:secret-hash\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(state, "identity.private.pem")); err != nil {
		t.Fatal(err)
	}
	s := service.Service{StateDir: state, SecretKeys: "*.private.pem"}
	r, err := copyFor(Options{Dest: dest, Out: io.Discard}, "mixy-pq-mixserver", s)
	if err != nil {
		return
	}
	b, _ := os.ReadFile(filepath.Join(r.Dir, "identity.private.pem"))
	if strings.Contains(string(b), "secret-hash") {
		t.Error("the backup followed a link in the state dir and copied a file from outside it")
	}
}
