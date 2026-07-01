package deb

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestARecordCommitDoesNotRestampTheCode(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", repo}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0=/dev/null")
		if b, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	write("cmd/kno/main.go", "package main\n")
	git("add", "-A")
	git("commit", "-qm", "code")
	code := ToolStamp(filepath.Join(repo))
	if code == "" {
		t.Fatal("no stamp")
	}
	time.Sleep(1100 * time.Millisecond)
	write("data/networks/namenlos/generated/deployments/reed.toml", "host = \"reed\"\n")
	git("add", "-A")
	git("commit", "-qm", "record")
	if got := ToolStamp(filepath.Join(repo, "data")); got != code {
		t.Fatalf("a deployment record restamped the code: %s, was %s; the cached kno package"+
			" then no longer matches and every later push stops", got, code)
	}
	write("cmd/kno/main.go", "package main\n\nfunc main() {}\n")
	git("add", "-A")
	git("commit", "-qm", "code again")
	if got := ToolStamp(repo); got == code {
		t.Fatal("a code commit did not change the stamp")
	}
}
