package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRootDoesNotReadAConfigFromAUserHome(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, ".local", "katzenpost", "kno.toml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("network = \"namenlos\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("KNO_CONFIG", "")
	saved := rootTrusts
	rootTrusts = func(string) bool { return false }
	defer func() { rootTrusts = saved }()
	if got := ResolvePath(""); got == p {
		t.Errorf("as root, kno reads the config %s from a home that root does not own", got)
	}
}
