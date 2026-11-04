package nodeedit

import (
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testPublicKeyFile(t *testing.T, scheme string) string {
	t.Helper()
	key := make([]byte, 64)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "public.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: strings.ToUpper(scheme) + " PUBLIC KEY", Bytes: key}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
