package source

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func upstreamFixture(t *testing.T) (dir, sha string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir = t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.hooksPath",
			"GIT_CONFIG_VALUE_0=/dev/null",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e",
			"GIT_AUTHOR_DATE=2023-11-14T22:13:20+0000",
			"GIT_COMMITTER_DATE=2023-11-14T22:13:20+0000")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "one")
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return dir, strings.TrimSpace(string(out))
}

func TestFetchFallsBackToThePinnedSHAWhenTheTagIsUnpushed(t *testing.T) {
	up, sha := upstreamFixture(t)
	work := t.TempDir()
	if err := fetchComponent(work, "katzenpost", up, "v0.0.105", sha, ""); err != nil {
		t.Fatalf("an unpushed tag with a pinned sha should still build: %v", err)
	}
	out, err := exec.Command("git", "-C", filepath.Join(work, "katzenpost"), "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != sha {
		t.Errorf("checked out %s, want the pinned %s", got, sha)
	}
}

func TestFetchStillFailsForAMissingRefWithNoPinnedSHA(t *testing.T) {
	up, _ := upstreamFixture(t)
	work := t.TempDir()
	err := fetchComponent(work, "katzenpost", up, "v0.0.105", "", "")
	if err == nil {
		t.Fatal("a ref that does not exist and no sha to fall back to must fail")
	}
	if !strings.Contains(err.Error(), "v0.0.105") {
		t.Errorf("error should name the ref: %v", err)
	}
}

func TestFetchRefusesAFallbackToTheWrongCommit(t *testing.T) {
	up, _ := upstreamFixture(t)
	work := t.TempDir()
	wrong := "0000000000000000000000000000000000000000"
	if err := fetchComponent(work, "katzenpost", up, "v0.0.105", wrong, ""); err == nil {
		t.Fatal("fell back to a sha this repository does not have")
	}
}

func TestFetchChecksOutTheComponentAtThePin(t *testing.T) {
	up, sha := upstreamFixture(t)
	work := t.TempDir()
	if err := fetchComponent(work, "katzenqt", up, "main", sha, ""); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(work, "katzenqt")
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != sha {
		t.Errorf("checked out %s, want the pinned %s", got, sha)
	}
}

func TestFetchRefusesAnExistingRefAtTheWrongCommit(t *testing.T) {
	up, _ := upstreamFixture(t)
	wrong := "0000000000000000000000000000000000000000"
	if err := fetchComponent(t.TempDir(), "katzenqt", up, "main", wrong, ""); err == nil {
		t.Fatal("accepted a checkout that is not the pinned commit")
	}
}

func TestFetchNeedsAUrlAndRef(t *testing.T) {
	up, _ := upstreamFixture(t)
	for _, c := range [][2]string{{"", "main"}, {up, ""}} {
		if err := fetchComponent(t.TempDir(), "katzenqt", c[0], c[1], "", ""); err == nil {
			t.Errorf("accepted url %q and ref %q", c[0], c[1])
		}
	}
}
