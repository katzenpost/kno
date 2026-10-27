package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func commitAll(t *testing.T, dir string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "one"}} {
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e",
			"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.hooksPath",
			"GIT_CONFIG_VALUE_0=/dev/null")
		if b, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	b, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func TestABuildMakesTheKatzenpostAndTheKnoSource(t *testing.T) {
	work := t.TempDir()
	pins := map[string]string{}
	for name, key := range map[string]string{"katzenpost": "KATZENPOST_SHA", "hpqc": "HPQC_SHA"} {
		d := filepath.Join(work, ".src", name)
		if err := os.MkdirAll(filepath.Dir(d), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(gitRepo(t), d); err != nil {
			t.Fatal(err)
		}
		pins[key] = commitAll(t, d)
	}
	repo := gitRepo(t)
	commitAll(t, repo)
	dir := t.TempDir()
	got, err := sourceTarballs(repo, work, dir, "0.0.107", "0.0.107-1+x", pins, 1700000000)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range got {
		names = append(names, filepath.Base(p))
	}
	want := "katzenpost-0.0.107-src.tar.gz katzenpost-0.0.107-hpqc-src.tar.gz" +
		" kno-0.0.107-1+x-src.tar.gz"
	if strings.Join(names, " ") != want {
		t.Errorf("made %v, want %s", names, want)
	}
	if !selfPackage("kno-0.0.107-1+x-src.tar.gz") {
		t.Error("the kno source counts as a recorded package")
	}
	if selfPackage("katzenpost-0.0.107-src.tar.gz") {
		t.Error("the katzenpost source does not count as a recorded package")
	}
}
