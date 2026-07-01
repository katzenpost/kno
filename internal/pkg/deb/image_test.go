package deb

import (
	"runtime"
	"strings"
	"testing"
)

func TestEveryArchWeBuildOnHasAPinnedPackager(t *testing.T) {

	for _, arch := range []string{"amd64", "arm64", "ppc64le"} {
		d, ok := debImageDigests[arch]
		if !ok {
			t.Errorf("no pinned dpkg-deb image for %s; packaging cannot run there", arch)
			continue
		}
		if !strings.HasPrefix(d, "sha256:") || len(d) != len("sha256:")+64 {
			t.Errorf("%s digest is not a sha256: %q", arch, d)
		}
	}
}

func TestPinnedPackagersAreDistinct(t *testing.T) {
	seen := map[string]string{}
	for arch, d := range debImageDigests {
		if prev, dup := seen[d]; dup {
			t.Errorf("%s and %s share a digest; one of them runs the wrong architecture", arch, prev)
		}
		seen[d] = arch
	}
}

func TestImageFollowsTheHostNotTheTarget(t *testing.T) {
	got, err := Settings{}.image()
	if err != nil {
		t.Fatalf("no packager for the host arch %s: %v", runtime.GOARCH, err)
	}
	want := debImageDigests[runtime.GOARCH]
	if !strings.HasSuffix(got, want) {
		t.Errorf("image = %s, want the %s digest %s", got, runtime.GOARCH, want)
	}
}

func TestExplicitImageWins(t *testing.T) {
	got, err := Settings{Image: "localhost/mine:latest"}.image()
	if err != nil {
		t.Fatal(err)
	}
	if got != "localhost/mine:latest" {
		t.Errorf("image = %s, want the override", got)
	}
}

func TestUnpinnedArchIsAnError(t *testing.T) {
	saved := debImageDigests
	debImageDigests = map[string]string{"nothinghere": "sha256:x"}
	t.Cleanup(func() { debImageDigests = saved })
	_, err := Settings{}.image()
	if err == nil {
		t.Fatal("an architecture with no pinned packager should be an error")
	}
	if !strings.Contains(err.Error(), runtime.GOARCH) {
		t.Errorf("the error does not name the architecture: %v", err)
	}
}
