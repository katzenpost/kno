package activate

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRootGitRunsNoCommandFromTheRepoConfig(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo := t.TempDir()
	mark := filepath.Join(t.TempDir(), "ran")
	hook := filepath.Join(repo, "fsmon")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch "+mark+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		c := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@invalid",
			"-c", "core.hooksPath=/dev/null"}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "x")
	git("config", "core.fsmonitor", hook)
	gitAsRoot(repo, "status", "--porcelain", "--untracked-files=all")
	if _, err := os.Stat(mark); err == nil {
		t.Error("a git call that root makes ran the core.fsmonitor command of the repo")
	}
}
