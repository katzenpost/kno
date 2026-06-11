package source

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.hooksPath",
		"GIT_CONFIG_VALUE_0=/dev/null",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func makeFixtureRepo(t *testing.T, base, name, patchDir string) string {
	t.Helper()
	repo := filepath.Join(base, name+".git")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("base "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-qm", "base")
	git(t, repo, "tag", "v1")

	if err := os.WriteFile(filepath.Join(repo, "after.txt"), []byte("past the pin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-qm", "after the tag")

	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("base "+name+"\npatched\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	diff := exec.Command("git", "diff")
	diff.Dir = repo
	out, err := diff.Output()
	if err != nil {
		t.Fatalf("git diff: %v", err)
	}
	if err := os.MkdirAll(patchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(patchDir, "0001.patch"), out, 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "checkout", "--", "a.txt")
	return repo
}

func fixtures(t *testing.T, tmp string, useSrc bool) (map[string]string, string) {
	t.Helper()
	patches := filepath.Join(tmp, "patches")
	kat := makeFixtureRepo(t, tmp, "katzenpost", filepath.Join(patches, "katzenpost"))
	hpqc := makeFixtureRepo(t, tmp, "hpqc", filepath.Join(patches, "hpqc"))
	pins := map[string]string{
		"GO_VERSION":     "1.26.2",
		"KATZENPOST_URL": "file://" + kat,
		"KATZENPOST_REF": "v1",
		"HPQC_URL":       "file://" + hpqc,
		"HPQC_REF":       "v1",
	}
	if useSrc {
		pins["KATZENPOST_SRC"] = kat
		pins["HPQC_SRC"] = hpqc
	}
	return pins, patches
}

func TestMaterializeSourceBuildsTheWorkdir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	tmp := t.TempDir()
	pins, patches := fixtures(t, tmp, true)
	work := filepath.Join(tmp, "work")
	if _, err := MaterializeSource(work, pins, patches); err != nil {
		t.Fatalf("MaterializeSource: %v", err)
	}

	for _, c := range []string{"katzenpost", "hpqc"} {
		body, err := os.ReadFile(filepath.Join(work, c, "a.txt"))
		if err != nil {
			t.Fatalf("%s: %v", c, err)
		}

		if !strings.Contains(string(body), "patched") {
			t.Errorf("%s: patch not applied: %q", c, body)
		}

		if _, err := os.Stat(filepath.Join(work, c, "after.txt")); err == nil {
			t.Errorf("%s: materialized past the pinned ref", c)
		}

		out, err := exec.Command("git", "-C", filepath.Join(work, c), "status", "--porcelain").Output()
		if err != nil {
			t.Fatalf("%s is not a git repo: %v", c, err)
		}
		if len(out) == 0 {
			t.Errorf("%s: the patch left no working-tree change against the base commit", c)
		}
	}

	gowork, err := os.ReadFile(filepath.Join(work, "go.work"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"go 1.26.2",
		"use ./katzenpost",
		"replace github.com/katzenpost/hpqc => ./hpqc",
	} {
		if !strings.Contains(string(gowork), want) {
			t.Errorf("go.work missing %q:\n%s", want, gowork)
		}
	}
}

func TestMaterializeSourceClonesWithoutLocalSource(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	tmp := t.TempDir()
	pins, patches := fixtures(t, tmp, false)
	work := filepath.Join(tmp, "work")
	if _, err := MaterializeSource(work, pins, patches); err != nil {
		t.Fatalf("MaterializeSource: %v", err)
	}
	for _, c := range []string{"katzenpost", "hpqc"} {
		if _, err := os.Stat(filepath.Join(work, c, "a.txt")); err != nil {
			t.Errorf("%s was not cloned: %v", c, err)
		}

		if _, err := os.Stat(filepath.Join(work, ".clone-"+c)); !os.IsNotExist(err) {
			t.Errorf("%s: scratch clone left behind", c)
		}
	}
}

func TestMaterializeSourceIsIdempotent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	tmp := t.TempDir()
	pins, patches := fixtures(t, tmp, true)
	work := filepath.Join(tmp, "work")
	if _, err := MaterializeSource(work, pins, patches); err != nil {
		t.Fatalf("first MaterializeSource: %v", err)
	}
	if _, err := MaterializeSource(work, pins, patches); err != nil {
		t.Fatalf("second MaterializeSource: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(work, "katzenpost", "a.txt"))
	if err != nil {
		t.Fatal(err)
	}

	if n := strings.Count(string(body), "patched"); n != 1 {
		t.Errorf("patch applied %d times, want once: %q", n, body)
	}
}

func TestParsePinsSkipsCommentsAndBlanks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pins.env")
	body := "# a comment\n\nGO_VERSION=1.26.2\n  KATZENPOST_REF=v0.0.104  \nnot a pin\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	pins, err := ParsePins(path)
	if err != nil {
		t.Fatal(err)
	}
	if pins["GO_VERSION"] != "1.26.2" || pins["KATZENPOST_REF"] != "v0.0.104" {
		t.Errorf("parsed %v", pins)
	}
	if len(pins) != 2 {
		t.Errorf("parsed %d pins, want 2: %v", len(pins), pins)
	}
}

func TestTheCheckoutKeepsTheTagAndTheRealCommit(t *testing.T) {
	needGit(t)
	upstream := t.TempDir()
	mustRun(t, "", "git", "init", "-q", upstream)
	mustRun(t, upstream, "git", "-c", "core.hooksPath=/dev/null", "-c", "user.name=t", "-c", "user.email=t@t",
		"commit", "--allow-empty", "-q", "-m", "one")
	mustRun(t, upstream, "git", "tag", "v9.9.9")
	want := out(t, upstream, "git", "rev-parse", "v9.9.9^{commit}")

	work := t.TempDir()
	if err := fetchComponent(work, "katzenpost", "", "v9.9.9", "", upstream); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(work, "katzenpost")

	if got := out(t, dest, "git", "rev-parse", "HEAD"); got != want {
		t.Errorf("HEAD = %s, want the upstream commit %s", got, want)
	}

	if got := out(t, dest, "git", "describe", "--tags", "--exact-match", "HEAD"); got != "v9.9.9" {
		t.Errorf("describe = %q, want v9.9.9", got)
	}

	if err := fetchComponent(work, "katzenpost", "", "v9.9.9", "", upstream); err != nil {
		t.Fatalf("second materialization: %v", err)
	}
}

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

func mustRun(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, b)
	}
}

func out(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	b, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s %v: %v", name, args, err)
	}
	return strings.TrimSpace(string(b))
}

func TestAPinThatCouldBeAGitOptionIsRefused(t *testing.T) {
	needGit(t)
	work := t.TempDir()
	upstream := t.TempDir()
	mustRun(t, "", "git", "init", "-q", upstream)
	mustRun(t, upstream, "git", "-c", "core.hooksPath=/dev/null", "-c", "user.name=t", "-c", "user.email=t@t",
		"commit", "--allow-empty", "-q", "-m", "x")

	for _, ref := range []string{
		"--upload-pack=/bin/false",
		"--help",
		"-v",
		"v1.0.0 --upload-pack=/bin/false",
		"..",
		"refs/tags/../../etc",
		"",
	} {
		err := fetchComponent(work, "katzenpost", "", ref, "", upstream)
		if err == nil {
			t.Errorf("pinned ref %q was accepted", ref)
			continue
		}
		if !strings.Contains(err.Error(), "ref") && !strings.Contains(err.Error(), "option") {
			t.Errorf("ref %q refused for the wrong reason: %v", ref, err)
		}
	}

	if err := fetchComponent(work, "katzenpost", "-u/bin/false", "v1.0.0", "", ""); err == nil {
		t.Error("a clone source beginning with '-' was accepted")
	}
}

func TestAPinnedCheckoutIsReusedWithoutTheNetwork(t *testing.T) {
	up := filepath.Join(t.TempDir(), "up")
	if err := os.MkdirAll(up, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, up, "init", "-q")
	if err := os.WriteFile(filepath.Join(up, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, up, "add", "main.go")
	git(t, up, "commit", "-qm", "one")
	git(t, up, "tag", "v1")
	out, err := exec.Command("git", "-C", up, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.TrimSpace(string(out))

	work := t.TempDir()
	git(t, work, "clone", "-q", up, "katzenpost")
	dest := filepath.Join(work, "katzenpost")
	if err := os.WriteFile(filepath.Join(dest, "main.go"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "stray"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := fetchComponent(work, "katzenpost", "https://kno.invalid/katzenpost", "v1", sha, ""); err != nil {
		t.Fatalf("a checkout that holds the pinned commit went to the network: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "main.go")); string(b) != "package main\n" {
		t.Errorf("an edited file survived the reuse: %q", b)
	}
	if _, err := os.Stat(filepath.Join(dest, "stray")); err == nil {
		t.Error("an untracked file survived the reuse")
	}
}
