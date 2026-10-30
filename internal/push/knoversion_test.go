package push

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/katzenpost/kno/internal/build"
)

func TestARollbackShipsTheKnoItsReleaseWasBuiltWith(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "thirdparty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "thirdparty", "pins.env"),
		[]byte("KATZENPOST_REF=v0.0.105\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-qm", "seed"}} {
		c := exec.Command("git", append([]string{"-C", repo}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if b, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	if v := KnoVersionFor(repo, "v0.0.104"); v != "" {
		t.Errorf("pushing v0.0.104 requires kno %s; a rollback can only ship the kno that release has", v)
	}
	if v := KnoVersionFor(repo, "v0.0.105"); v == "" {
		t.Error("pushing the pinned release no longer requires the kno this repo builds")
	}
}

func TestPushAndBuildRequireTheSameKnoVersion(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "thirdparty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "thirdparty", "pins.env"), []byte("KATZENPOST_REF=v0.0.107\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := KnoVersionFor(repo, "v0.0.107"), build.ToolVersion(repo, "n", "v0.0.107"); got != want {
		t.Fatalf("push requires %s but build produces %s", got, want)
	}
}
