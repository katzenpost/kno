package fsutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyTreeRefusesASymlink(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "identity.private.pem")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY BYTES"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	if err := os.Symlink(secret, filepath.Join(src, "notes.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	dst := filepath.Join(t.TempDir(), "staged")

	err := CopyTreeGuarded(src, dst)
	if err == nil {
		t.Fatal("a symlink in a key source was copied")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("the error does not say what was wrong: %v", err)
	}

	if b, err := os.ReadFile(filepath.Join(dst, "notes.txt")); err == nil {
		if strings.Contains(string(b), "PRIVATE KEY") {
			t.Error("the key's bytes were staged under a harmless name")
		}
	}
}

func TestCopyTreeStillCopiesPublicKeys(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "x_id_pub_key.pem"), []byte("PUBLIC"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "staged")
	if err := CopyTreeGuarded(src, dst); err != nil {
		t.Fatalf("a public key was refused: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "x_id_pub_key.pem")); err != nil {
		t.Errorf("the public key was not staged: %v", err)
	}
}
