package source

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestALocalCloneSurvivesADifferentFilesystem(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	other := "/dev/shm"
	fi, err := os.Stat(other)
	if err != nil || !fi.IsDir() {
		t.Skip("no /dev/shm to use as a second filesystem")
	}
	if sameDevice(t, other, t.TempDir()) {
		t.Skip("/dev/shm is on the same device as the temp dir; nothing to prove here")
	}

	src := t.TempDir()
	gitRun(t, "", "init", "-q", "--initial-branch=main", src)
	if err := os.WriteFile(filepath.Join(src, "f"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, src, "add", "-A")
	gitRun(t, src, "-c", "core.hooksPath=/dev/null", "-c", "user.name=t", "-c", "user.email=t@invalid", "commit", "-q", "-m", "one")
	gitRun(t, src, "tag", "v0.0.1")

	work, err := os.MkdirTemp(other, "kno-xdev-")
	if err != nil {
		t.Skipf("cannot write to %s: %v", other, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(work) })

	if err := fetchComponent(work, "katzenpost", "", "v0.0.1", "", src); err != nil {
		t.Fatalf("a local clone onto another filesystem failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(work, "katzenpost", ".git")); err != nil {
		t.Errorf("no clone landed in the work directory: %v", err)
	}
}

func sameDevice(t *testing.T, a, b string) bool {
	t.Helper()
	out, err := exec.Command("stat", "-c", "%d", a, b).Output()
	if err != nil {
		return true
	}
	f := strings.Fields(string(out))
	return len(f) < 2 || f[0] == f[1]
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
