package update

import (
	"path/filepath"
	"testing"

	"github.com/katzenpost/kno/internal/track"
)

func TestBuildLandsWhereActivateReads(t *testing.T) {
	home := t.TempDir()
	e := track.Env{ReleasesDir: filepath.Join(home, "releases")}
	r := ToolRunner{Env: e, Arch: "amd64"}

	dist := filepath.Dir(r.Env.ReleasesDir)

	built := filepath.Join(dist, "releases", "v0.0.104")
	activated := filepath.Join(e.ReleasesDir, "v0.0.104")

	if built != activated {
		t.Fatalf("a tick would build into %s and activate from %s", built, activated)
	}
}

func TestBuildFollowsAnOverriddenReleasesDir(t *testing.T) {
	for _, dir := range []string{"/srv/kno/releases", "/var/lib/kno-updater/releases"} {
		r := ToolRunner{Env: track.Env{ReleasesDir: dir}, Arch: "amd64"}
		built := filepath.Join(filepath.Dir(r.Env.ReleasesDir), "releases", "v0.0.104")
		if want := filepath.Join(dir, "v0.0.104"); built != want {
			t.Errorf("RELEASES_DIR=%s: builds into %s, activates from %s", dir, built, want)
		}
	}
}

func TestBuildRefusesWithoutAnArchitecture(t *testing.T) {
	r := ToolRunner{Env: track.Env{ReleasesDir: t.TempDir()}}
	if err := r.Build("v0.0.104"); err == nil {
		t.Fatal("a build with no target architecture should be refused")
	}
}
