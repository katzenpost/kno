package record

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	run(t, dir, "config", "user.email", "t@example.invalid")
	run(t, dir, "config", "user.name", "t")
	run(t, dir, "config", "core.hooksPath", "/dev/null")
	if err := os.WriteFile(filepath.Join(dir, "seed"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "add", "-A")
	run(t, dir, "commit", "-qm", "seed")
	return dir
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if b, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, b)
	}
}

func out(t *testing.T, dir string, args ...string) string {
	t.Helper()
	b, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(b))
}

func seedRecord(t *testing.T, repo, network, host, body string) string {
	t.Helper()
	p := Path(repo, network, host)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLandMakesOneMergeCommitPerHost(t *testing.T) {
	repo := gitRepo(t)
	seedRecord(t, repo, "n", "mixy", "host = \"mixy\"\n")

	if ok, err := Land(repo, "n", "mixy", "mixy: v0.0.105"); err != nil || !ok {
		t.Fatalf("Land = %v %v, want a commit", ok, err)
	}
	if b := out(t, repo, "rev-parse", "--abbrev-ref", "HEAD"); b != "main" {
		t.Fatalf("left on %q, want main", b)
	}
	if p := out(t, repo, "rev-list", "--parents", "-n", "1", "HEAD"); len(strings.Fields(p)) != 3 {
		t.Errorf("HEAD is not a merge commit: %s", p)
	}
	files := out(t, repo, "show", "--name-only", "--format=", "HEAD^2")
	if files != "networks/n/.generated/deployments/mixy.toml" {
		t.Errorf("the merge carries %q", files)
	}
	if s := out(t, repo, "status", "--porcelain"); s != "" {
		t.Errorf("the tree is not clean after landing: %q", s)
	}
}

func TestLandAgainRecordsTheRollback(t *testing.T) {
	repo := gitRepo(t)
	seedRecord(t, repo, "n", "mixy", "host = \"mixy\"\nversion = \"v0.0.105\"\n")
	if _, err := Land(repo, "n", "mixy", "mixy: v0.0.105"); err != nil {
		t.Fatal(err)
	}
	seedRecord(t, repo, "n", "mixy", "host = \"mixy\"\nversion = \"v0.0.104\"\n")
	if _, err := Land(repo, "n", "mixy", "mixy: rolled back to v0.0.104"); err != nil {
		t.Fatal(err)
	}
	if n := out(t, repo, "rev-list", "--count", "--merges", "HEAD"); n != "2" {
		t.Errorf("%s merge commits, want 2", n)
	}
	b, err := os.ReadFile(Path(repo, "n", "mixy"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "v0.0.104") {
		t.Errorf("the landed record is not the rollback: %s", b)
	}
}

func TestLandCommitsOnlyTheRecord(t *testing.T) {
	repo := gitRepo(t)
	seedRecord(t, repo, "n", "mixy", "host = \"mixy\"\n")
	if err := os.WriteFile(filepath.Join(repo, "mine.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "add", "--", "mine.txt")

	if _, err := Land(repo, "n", "mixy", "mixy: v0.0.105"); err != nil {
		t.Fatal(err)
	}
	if f := out(t, repo, "show", "--name-only", "--format=", "HEAD^2"); strings.Contains(f, "mine.txt") {
		t.Errorf("a staged file rode along: %s", f)
	}
	if s := out(t, repo, "status", "--porcelain"); !strings.Contains(s, "mine.txt") {
		t.Errorf("the operator's staged file was lost: %q", s)
	}
}

func TestLandIsQuietWhenNothingChanged(t *testing.T) {
	repo := gitRepo(t)
	seedRecord(t, repo, "n", "mixy", "host = \"mixy\"\n")
	if _, err := Land(repo, "n", "mixy", "first"); err != nil {
		t.Fatal(err)
	}
	head := out(t, repo, "rev-parse", "HEAD")
	ok, err := Land(repo, "n", "mixy", "again")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("Land says it committed an unchanged record, so kno logs a commit that it did not make")
	}
	if out(t, repo, "rev-parse", "HEAD") != head {
		t.Error("an unchanged record made a second merge commit")
	}
}

func cloneWithOrigin(t *testing.T) (repo, bare string) {
	t.Helper()
	repo = gitRepo(t)
	bare = filepath.Join(t.TempDir(), "origin.git")
	run(t, repo, "init", "-q", "--bare", "-b", "main", bare)
	run(t, repo, "remote", "add", "origin", bare)
	run(t, repo, "push", "-q", "-u", "origin", "main")
	return repo, bare
}

func TestLandAndPushPutsTheRecordInTheSharedRepository(t *testing.T) {
	repo, bare := cloneWithOrigin(t)
	seedRecord(t, repo, "n", "mixy", "host = \"mixy\"\n")
	ok, err := LandAndPush(repo, "n", "mixy", "mixy: v0.0.107")
	if err != nil || !ok {
		t.Fatalf("LandAndPush = %v %v", ok, err)
	}
	if got, want := out(t, bare, "rev-parse", "main"), out(t, repo, "rev-parse", "HEAD"); got != want {
		t.Errorf("origin has %s, the clone has %s", got, want)
	}
}

func TestAFailedPushLeavesTheCloneAFastForward(t *testing.T) {
	repo, bare := cloneWithOrigin(t)
	head := out(t, repo, "rev-parse", "HEAD")
	p := seedRecord(t, repo, "n", "mixy", "host = \"mixy\"\n")
	run(t, repo, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "nothing.git"))
	ok, err := LandAndPush(repo, "n", "mixy", "mixy: v0.0.107")
	if ok || err == nil || !strings.Contains(err.Error(), "removed the local commit") {
		t.Fatalf("LandAndPush = %v %v, want no commit and an error that says so", ok, err)
	}
	if got := out(t, repo, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD moved to %s after a failed push", got)
	}
	if s := out(t, repo, "diff", "--cached", "--name-only"); s != "" {
		t.Errorf("the record stays staged: %q", s)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("the record file is gone: %v", err)
	}

	run(t, repo, "remote", "set-url", "origin", bare)
	other := filepath.Join(t.TempDir(), "other")
	run(t, repo, "clone", "-q", bare, other)
	run(t, other, "config", "user.email", "t@example.invalid")
	run(t, other, "config", "user.name", "t")
	run(t, other, "config", "core.hooksPath", "/dev/null")
	run(t, other, "commit", "-q", "--allow-empty", "-m", "upstream")
	run(t, other, "push", "-q", "origin", "main")
	run(t, repo, "fetch", "-q", "origin")
	run(t, repo, "merge", "--ff-only", "-q", "@{u}")
}
