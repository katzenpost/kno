package deb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

func TestImageDigestsAgree(t *testing.T) {
	repo := testutil.RepoRoot(t)
	pins, err := os.ReadFile(filepath.Join(repo, "thirdparty", "pins.env"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for _, line := range strings.Split(string(pins), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			want[k] = v
		}
	}

	if got := want["DEB_IMAGE_DIGEST"]; got == "" {
		t.Error("pins.env has no DEB_IMAGE_DIGEST; the deb image would float")
	} else if got != debImageDigests["amd64"] {
		t.Errorf("deb image digest drift:\n  pins.env: %s\n  deb.Build: %s",
			got, debImageDigests["amd64"])
	}

	if want["BUILD_IMAGE_DIGEST"] == "" {
		t.Fatal("pins.env has no BUILD_IMAGE_DIGEST; the kno toolchain would float")
	}

	cf, err := os.ReadFile(filepath.Join(repo, "packaging", "Containerfile.build"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(cf), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "FROM ") {
			continue
		}
		if !strings.Contains(line, "${BASE}") {
			t.Errorf("Containerfile.build pins an image itself (%q); the target"+
				" distribution has to come from the caller", line)
		}
	}
}
