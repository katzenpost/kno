package activate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheKeyCopyDoesNotFollowALinkInTheOldStateDir(t *testing.T) {
	old, root := t.TempDir(), t.TempDir()
	newDir := filepath.Join(root, "new")
	outside := filepath.Join(t.TempDir(), "shadow")
	if err := os.WriteFile(outside, []byte("root:secret-hash\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "identity.private.pem"), []byte("key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(old, "loot.pem")); err != nil {
		t.Fatal(err)
	}
	if b, err := exec.Command("sh", "-c", copyForwardScript, "sh", old, newDir, "").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	b, _ := os.ReadFile(filepath.Join(newDir, "loot.pem"))
	if strings.Contains(string(b), "secret-hash") {
		t.Error("root copied the target of a link in the old state dir into the new one, which postinst then chowns to the service user")
	}
}
