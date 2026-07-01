package deb

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func commitFile(t *testing.T, repo, rel, body string) {
	t.Helper()
	p := filepath.Join(repo, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", rel}, {"commit", "-q", "-m", rel}} {
		c := exec.Command("git", args...)
		c.Dir = repo
		c.Env = append(c.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example",
			"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.hooksPath",
			"GIT_CONFIG_VALUE_0=/dev/null")
		if b, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
}

func TestToolStampMovesOnlyWithWhatTheBinaryCarries(t *testing.T) {
	repo := gitRepo(t)
	commitFile(t, repo, "internal/a/a.go", "package a\n")
	want := ToolStamp(repo)
	if !stampShape.MatchString(want) {
		t.Fatalf("ToolStamp = %q, want +<14 digits>.g<hash>", want)
	}
	for _, rel := range []string{
		"docs/x.md",
		"README.md",
		"internal/a/a_test.go",
		"data/networks/n/.generated/deployments/h.toml",
		"data/networks/n/generated/deployments/h.toml",
		"data/networks/n/.generated/releases/v1/packages.json",
		"data/networks/n/editable-configs/nodes/x.toml",
	} {
		commitFile(t, repo, rel, "x\n")
		if got := ToolStamp(repo); got != want {
			t.Errorf("a commit to %s moved the kno stamp from %s to %s", rel, want, got)
		}
	}
	for _, rel := range []string{"internal/a/b.go", "data/thirdparty/pins.env", "data/networks/n/.generated/configs/x.toml", "go.mod"} {
		before := ToolStamp(repo)
		commitFile(t, repo, rel, "y\n")
		if got := ToolStamp(repo); got == before {
			t.Errorf("a commit to %s left the kno stamp at %s", rel, got)
		}
	}
	sub := filepath.Join(repo, "data")
	if got, top := ToolStamp(sub), ToolStamp(repo); got != top {
		t.Errorf("from the data root the stamp is %s, from the top %s", got, top)
	}
}

func TestToolVersionIsDirtyOnlyForWhatTheBinaryCarries(t *testing.T) {
	repo := gitRepo(t)
	commitFile(t, repo, "internal/a/a.go", "package a\n")
	commitFile(t, repo, "docs/x.md", "x\n")
	if err := os.WriteFile(filepath.Join(repo, "docs", "x.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ToolVersion(repo, "v0.0.107"); strings.Contains(got, "dirty") {
		t.Errorf("a docs edit made the kno version %s", got)
	}
	if err := os.WriteFile(filepath.Join(repo, "internal", "a", "a.go"), []byte("package a\n\nvar X int\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ToolVersion(repo, "v0.0.107"); !strings.HasSuffix(got, ".dirty") {
		t.Errorf("an uncommitted code edit left the kno version clean: %s", got)
	}
}
