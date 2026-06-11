package fsutil

import "testing"

func TestMixKeysAreSecret(t *testing.T) {
	for _, name := range []string{
		"mixkey-12345.bin",
		"mixkey-0.bin",
		".mixkey-987654321",
	} {
		if !IsSecretKey(name) {
			t.Errorf("IsSecretKey(%q) = false; a Sphinx mix key is a secret key", name)
		}
	}
}

func TestMixKeyWideningDoesNotCatchPublicFiles(t *testing.T) {
	for _, name := range []string{
		"identity.public.pem",
		"mixkeys",
		"mixkey.md",
		"remixkey-notes.txt",
		"katzenpost_0.0.104_amd64.deb",
	} {
		if IsSecretKey(name) {
			t.Errorf("IsSecretKey(%q) = true; that is not a secret key", name)
		}
	}
}
