package track

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func writeVersions(t *testing.T, repo string, body string) {
	t.Helper()
	p := filepath.Join(repo, VersionsFile)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestVTagsComesFromTheCommittedFile(t *testing.T) {
	repo := t.TempDir()
	writeVersions(t, repo, "v0.0.104\nv0.0.106\nv0.0.105\n")
	e := Env{Repo: repo, Network: "namenlos",
		QuarantineFile: filepath.Join(t.TempDir(), "quarantine")}
	got := e.VTags()
	want := []string{"v0.0.106", "v0.0.105", "v0.0.104"}
	if len(got) != len(want) {
		t.Fatalf("VTags = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("VTags = %v, want %v (newest first)", got, want)
		}
	}
}

func TestVTagsIgnoresCommentsAndBlanksAndNonVersions(t *testing.T) {
	repo := t.TempDir()
	writeVersions(t, repo, "\nv0.0.105\nnot-a-version\n\nv0.0.104\n")
	e := Env{Repo: repo, Network: "namenlos",
		QuarantineFile: filepath.Join(t.TempDir(), "quarantine")}
	got := e.VTags()
	if len(got) != 2 || got[0] != "v0.0.105" || got[1] != "v0.0.104" {
		t.Fatalf("VTags = %v, want the 2 version lines", got)
	}
}

func TestAKnoGitTagIsNotAVersion(t *testing.T) {
	repo := knoRepoAt(t, "v0.0.104", "v0.0.104")
	e := Env{Repo: repo, Network: "namenlos",
		QuarantineFile: filepath.Join(t.TempDir(), "quarantine")}
	if got := e.VTags(); len(got) != 0 {
		t.Fatalf("VTags = %v, want nothing: a kno tag is not a version", got)
	}
}

func TestAVersionWithoutAKatzenpostTagIsNotATarget(t *testing.T) {
	repo := t.TempDir()
	writeVersions(t, repo, "v0.0.105\n")
	e := Env{Repo: repo, Network: "namenlos",
		KatzenpostRepo: t.TempDir(),
		QuarantineFile: filepath.Join(t.TempDir(), "quarantine")}
	if e.KPHasTag("v0.0.105") {
		t.Error("a version with no katzenpost tag passed")
	}
	if got, _ := e.Target(); got != "" {
		t.Errorf("Target = %q, want none", got)
	}
}

func knoRepoAt(t *testing.T, tag, upstreamRef string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example",
			"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.hooksPath",
			"GIT_CONFIG_VALUE_0=/dev/null",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example")
		if b, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.MkdirAll(filepath.Join(dir, "thirdparty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "thirdparty", "pins.env"),
		[]byte("KATZENPOST_REF="+upstreamRef+"\nKATZENPOST_SHA=deadbeef\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "pins")
	run("tag", tag)
	return dir
}
