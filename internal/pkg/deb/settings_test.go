package deb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvIsReadInOnePlace(t *testing.T) {
	dir := "."
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		n := e.Name()
		if !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") || n == "settings.go" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "os.Getenv") {
			t.Errorf("%s reads os.Getenv directly; route it through Settings/EnvSettings", n)
		}
	}
}

func TestSettingsDefaults(t *testing.T) {
	var s Settings
	if s.epoch() != DefaultEpoch {
		t.Errorf("epoch default: %s", s.epoch())
	}
	img, err := s.image()
	if err != nil {
		t.Fatalf("no packager pinned for this architecture: %v", err)
	}
	if !strings.Contains(img, "sha256:") {
		t.Errorf("image default is not digest-pinned: %s", img)
	}
	if !strings.Contains(s.maintainer(), "packages@katzenpost.network") {
		t.Errorf("maintainer default: %s", s.maintainer())
	}
	explicit := Settings{SourceDateEpoch: "42", Image: "img", Maintainer: "m"}
	eimg, err := explicit.image()
	if err != nil {
		t.Fatal(err)
	}
	if explicit.epoch() != "42" || eimg != "img" ||
		explicit.maintainer() != "m" {
		t.Error("explicit settings must win over the defaults")
	}
}
