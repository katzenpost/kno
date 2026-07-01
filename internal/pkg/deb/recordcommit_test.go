package deb

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func repoWithOrigin(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	origin := filepath.Join(dir, "origin.git")
	work := filepath.Join(dir, "work")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@invalid",
			"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.hooksPath",
			"GIT_CONFIG_VALUE_0=/dev/null",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "--quiet", "--bare", "--initial-branch=main", origin)
	run("init", "--quiet", "--initial-branch=main", work)
	run("-C", work, "remote", "add", "origin", origin)
	write(t, work, "README.md", "start\n")

	write(t, work, recordPath, "tag = \"v0.0.103\"\n")
	run("-C", work, "add", "-A")
	run("-C", work, "commit", "--quiet", "-m", "start")

	run("-C", work, "tag", "v0.0.104")
	run("-C", work, "push", "--quiet", "-u", "origin", "main")
	run("-C", work, "push", "--quiet", "origin", "v0.0.104")
	return work
}

func write(t *testing.T, repo, rel, body string) {
	t.Helper()
	p := filepath.Join(repo, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commit(t *testing.T, repo, msg string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"commit", "--quiet", "-m", msg}} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@invalid",
			"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.hooksPath",
			"GIT_CONFIG_VALUE_0=/dev/null",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

const recordPath = "networks/namenlos/.generated/deployments/lilly.toml"

func TestAPushedTreeIsClean(t *testing.T) {
	repo := repoWithOrigin(t)
	if isDirty(repo) {
		t.Error("a checkout matching origin/main reported dirty")
	}
}

func TestCommittingTheDeploymentRecordsKeepsTheBuildClean(t *testing.T) {
	repo := repoWithOrigin(t)
	write(t, repo, recordPath, "tag = \"v0.0.104\"\n")

	if isDirty(repo) {
		t.Fatal("an UNCOMMITTED deployment record reported dirty;" +
			" that half of the exemption regressed")
	}
	commit(t, repo, "update nodes")
	if isDirty(repo) {
		t.Error("committing the deployment records made the build dirty;" +
			" kno update writes them and asks for exactly that commit")
	}
	if rev := RepoRevision(repo, "v0.0.104"); rev == "" || containsDirty(rev) {
		t.Errorf("RepoRevision = %q after committing records; want no +dirty", rev)
	}
}

func TestOnlyAnUncommittedCodeChangeIsDirty(t *testing.T) {
	repo := repoWithOrigin(t)
	write(t, repo, "internal/thing.go", "package thing\n")
	if !isDirty(repo) {
		t.Error("an uncommitted source change reported clean")
	}
	if rev := RepoRevision(repo, "v0.0.104"); !containsDirty(rev) {
		t.Errorf("RepoRevision = %q for uncommitted work; want +dirty", rev)
	}
	commit(t, repo, "add a thing")
	if isDirty(repo) {
		t.Error("an unpushed source commit reported dirty")
	}
}

func TestUncommittedSourceNextToRecordsIsDirty(t *testing.T) {
	repo := repoWithOrigin(t)
	write(t, repo, recordPath, "tag = \"v0.0.104\"\n")
	write(t, repo, "internal/thing.go", "package thing\n")
	if !isDirty(repo) {
		t.Error("an uncommitted source change next to records reported clean")
	}
}

func containsDirty(v string) bool {
	return len(v) >= 6 && v[len(v)-6:] == "+dirty"
}

func TestAnEmptyUnpushedCommitIsClean(t *testing.T) {
	repo := repoWithOrigin(t)
	cmd := exec.Command("git", "-C", repo, "commit", "-q", "--allow-empty", "-m", "nothing")
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@invalid",
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.hooksPath",
		"GIT_CONFIG_VALUE_0=/dev/null",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@invalid")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	if isDirty(repo) {
		t.Error("an empty unpushed commit reported dirty")
	}
}
