package build

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/katzenpost/kno/internal/track"
)

func TestBuildUsesTheCheckoutThatUpdateUses(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	if err := os.MkdirAll(filepath.Join(data, "networks"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := exec.Command("git", "-C", root, "init", "-q", "-b", "main")
	if b, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, b)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KNO_HOME", home)
	t.Setenv("KNO_REPO", "")
	t.Setenv("KNO_URL", filepath.Join(home, "no-origin"))
	t.Chdir(root)
	got, err := buildRepository(false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	e, err := track.LoadEnv()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.EnsureKnoRepo(track.Logger{Tool: "t", W: io.Discard}); err != nil {
		t.Fatal(err)
	}
	if got != data || got != e.Repo {
		t.Fatalf("build=%s update=%s want=%s", got, e.Repo, data)
	}
	if _, err := os.Stat(track.CloneDir(home)); !os.IsNotExist(err) {
		t.Fatalf("created a second checkout: %v", err)
	}
}
