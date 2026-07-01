package deb

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(c.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example",
			"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.hooksPath",
			"GIT_CONFIG_VALUE_0=/dev/null")
		if b, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	run("init", "-q", "-b", "main")
	run("commit", "-q", "--allow-empty", "-m", "one")
	return dir
}

var stampShape = regexp.MustCompile(`^\+\d{14}\.g[0-9a-f]+$`)

func TestToolVersionOrdersAboveTheUnstampedAndDirtyLast(t *testing.T) {
	repo := gitRepo(t)
	commitFile(t, repo, "internal/a/a.go", "package a\n")
	clean := ToolVersion(repo, "v0.0.105")
	if strings.Contains(clean, "dirty") {
		t.Errorf("%q says dirty for a clean tree", clean)
	}
	if CompareVersions(clean, "0.0.105-1") <= 0 {
		t.Errorf("%s does not order above 0.0.105-1", clean)
	}
	got := ToolVersionRev(repo, "v0.0.105", "1+dirty")
	if strings.Contains(got, "dirty") {
		t.Errorf("%q carries the revision's whole-tree marker; only the tool paths decide", got)
	}
}

func TestToolVersionLeavesTheBaseAloneOutsideACheckout(t *testing.T) {
	if got := ToolVersionRev(t.TempDir(), "v0.0.105", "1"); got != "0.0.105-1" {
		t.Errorf("ToolVersionRev = %q, want the base unchanged", got)
	}
}
