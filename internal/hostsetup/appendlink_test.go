package hostsetup

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestAppendingToAnUpdaterFileDoesNotFollowItsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "shadow")
	if err := os.WriteFile(target, []byte("root:x:0:0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ssh := filepath.Join(dir, "home", ".ssh")
	if err := os.MkdirAll(ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	keys := filepath.Join(ssh, "authorized_keys")
	if err := os.Symlink(target, keys); err != nil {
		t.Fatal(err)
	}
	f := File{Path: keys, Content: "ssh-ed25519 AAAA operator\n", Mode: 0o600, Append: true}
	_ = f.write(context.Background(), io.Discard)
	b, _ := os.ReadFile(target)
	fi, _ := os.Stat(target)
	if string(b) != "root:x:0:0\n" || fi.Mode().Perm() != 0o644 {
		t.Errorf("root wrote through the updater's symlink: %s is now mode %04o and holds %q",
			target, fi.Mode().Perm(), b)
	}
}
