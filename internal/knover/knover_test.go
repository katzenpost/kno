package knover

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@invalid",
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func commitFile(t *testing.T, dir, path, body string) {
	t.Helper()
	p := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", path)
	git(t, dir, "commit", "-q", "-m", path)
}

func TestARecordCommitDoesNotCountAsARevision(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	commitFile(t, dir, "internal/a.go", "package a\n")
	data := filepath.Join(dir, "data")
	if got := Revision(data, "v0.0.107"); got != "1" {
		t.Errorf("no tag: revision %q, want 1", got)
	}
	git(t, dir, "tag", "v0.0.107")
	if got := Revision(data, "v0.0.107"); got != "1" {
		t.Errorf("at the tag: revision %q, want 1", got)
	}
	commitFile(t, dir, "data/networks/namenlos/.generated/deployments/mixy.toml", "a\n")
	commitFile(t, dir, "data/networks/namenlos/.generated/releases/v0.0.107/SHA256SUMS", "b\n")
	if got := Revision(data, "v0.0.107"); got != "1" {
		t.Errorf("after two record commits: revision %q, want 1", got)
	}
	commitFile(t, dir, "internal/b.go", "package a\n")
	if got := Revision(data, "v0.0.107"); got != "2" {
		t.Errorf("after a code commit: revision %q, want 2", got)
	}
}

func TestAnOriginMainWithoutTheTagIsNotCounted(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	commitFile(t, dir, "old.go", "package old\n")
	git(t, dir, "branch", "-m", "old")
	git(t, dir, "update-ref", "refs/remotes/origin/main", "old")
	git(t, dir, "checkout", "-q", "--orphan", "new")
	git(t, dir, "rm", "-q", "-r", "--cached", ".")
	commitFile(t, dir, "internal/a.go", "package a\n")
	git(t, dir, "tag", "v0.0.107")
	commitFile(t, dir, "data/networks/namenlos/.generated/deployments/mixy.toml", "a\n")
	data := filepath.Join(dir, "data")
	count := func() string { return strings.TrimSuffix(Revision(data, "v0.0.107"), "+dirty") }
	if got := count(); got != "1" {
		t.Errorf("an origin/main from another history: revision %q, want 1", got)
	}
	commitFile(t, dir, "internal/b.go", "package a\n")
	if got := count(); got != "2" {
		t.Errorf("after a code commit: revision %q, want 2", got)
	}
}

func TestADirtyTreeMarksTheRevision(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	commitFile(t, dir, "internal/a.go", "package a\n")
	commitFile(t, dir, "data/networks/n/editable-configs/arch.toml", "default = \"amd64\"\n")
	if got := Revision(filepath.Join(dir, "data"), "v0.0.107"); got != "1" {
		t.Errorf("a clean tree: revision %q, want 1", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "a.go"), []byte("package b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Revision(filepath.Join(dir, "data"), "v0.0.107"); got != "1+dirty" {
		t.Errorf("an edited tree: revision %q, want 1+dirty, so kno build record refuses it", got)
	}
}

func TestAHeadBehindOriginMainCountsOnlyItsOwnCommits(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	commitFile(t, dir, "data/networks/n/editable-configs/arch.toml", "default = \"amd64\"\n")
	git(t, dir, "tag", "v0.0.107")
	commitFile(t, dir, "internal/b.go", "package a\n")
	data := filepath.Join(dir, "data")
	count := func() string { return strings.TrimSuffix(Revision(data, "v0.0.107"), "+dirty") }
	git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	if got := count(); got != "2" {
		t.Errorf("HEAD at origin/main: revision %q, want 2", got)
	}
	commitFile(t, dir, "internal/c.go", "package a\n")
	git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	git(t, dir, "reset", "-q", "--hard", "HEAD~1")
	if got := count(); got != "2" {
		t.Errorf("HEAD one code commit behind origin/main: revision %q, want 2", got)
	}
	commitFile(t, dir, "internal/d.go", "package a\n")
	if got := count(); got != "2" {
		t.Errorf("a local commit that origin/main does not have: revision %q, want 2", got)
	}
}

func TestARecordedReleaseGivesItsToolRevision(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	commitFile(t, dir, "data/networks/namenlos/editable-configs/arch.toml", "default = \"amd64\"\n")
	git(t, dir, "tag", "v0.0.107")
	commitFile(t, dir, "internal/b.go", "package a\n")
	data := filepath.Join(dir, "data")
	if got := Revision(data, "v0.0.107"); got != "2" {
		t.Fatalf("one code commit after the tag: revision %q, want 2", got)
	}
	commitFile(t, dir, "data/networks/namenlos/.generated/releases/v0.0.107/packages.json",
		`{"network": "namenlos", "tag": "v0.0.107", "tool_revision": "7"}`+"\n")
	if got := Revision(data, "v0.0.107"); got != "7" {
		t.Errorf("a recorded release: revision %q, want its tool_revision 7, the one kno build gives kno", got)
	}
	if got := Revision(data, "v0.0.106"); got != "1" {
		t.Errorf("a tag with no record and no git tag: revision %q, want 1", got)
	}
}
