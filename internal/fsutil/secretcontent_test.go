package fsutil

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestTheGuardedCopyChecksContentNotOnlyNames(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	key := "-----BEGIN X25519 PRIVATE KEY-----\nAAAA\n-----END X25519 PRIVATE KEY-----\n"
	if err := os.WriteFile(filepath.Join(src, "mixy.link.pem"), []byte(key), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CopyTreeGuarded(src, dst); err == nil {
		t.Error("CopyTreeGuarded copied a private key named mixy.link.pem into a package tree")
	}
	if f, _ := FindSecret(src); f == "" {
		t.Error("FindSecret does not see the private key mixy.link.pem")
	}
}

func TestASecretAfterTheFirstMegabyteIsSeen(t *testing.T) {
	p := filepath.Join(t.TempDir(), "mixy.link.pem")
	body := append(bytes.Repeat([]byte("#\n"), 1<<19+1), []byte("-----BEGIN X25519 PRIVATE KEY-----\nAAAA\n-----END X25519 PRIVATE KEY-----\n")...)
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if !IsSecretKeyFile(p) {
		t.Error("IsSecretKeyFile misses a private key that starts after 1 MiB")
	}
}
