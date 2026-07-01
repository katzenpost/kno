package deb

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func push(t *testing.T, repo string) {
	t.Helper()
	cmd := exec.Command("git", "-C", repo, "push", "--quiet", "origin", "main")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git push: %v\n%s", err, out)
	}
}

func TestAnUnpushedConfigCommitIsClean(t *testing.T) {
	repo := repoWithOrigin(t)
	const svc = "mixy-pq-mixserver"
	cfg := "networks/namenlos/.generated/configs/" + svc + ".toml"
	write(t, repo, cfg, "a\n")
	commit(t, repo, "mixy a")
	push(t, repo)
	write(t, repo, cfg, "b\n")
	commit(t, repo, "mixy b")
	if s := ConfigStamp(repo, "namenlos", svc); s == "" || DirtyName(s) {
		t.Fatalf("an unpushed config commit gave the stamp %q, want a clean stamp", s)
	}
	write(t, repo, cfg, "c\n")
	if s := ConfigStamp(repo, "namenlos", svc); !strings.HasSuffix(s, ".dirty") {
		t.Errorf("an uncommitted config change gave the stamp %q, want the .dirty mark", s)
	}
}

func TestAnUnpushedCodeCommitIsClean(t *testing.T) {
	repo := repoWithOrigin(t)
	write(t, repo, "internal/a/a.go", "package a\n")
	commit(t, repo, "a")
	push(t, repo)
	write(t, repo, "internal/a/b.go", "package a\n")
	commit(t, repo, "b")
	if v := ToolVersionRev(repo, "v0.0.104", "1"); DirtyName(v) {
		t.Fatalf("an unpushed code commit gave the kno version %s, want a clean version", v)
	}
	write(t, repo, "internal/a/c.go", "package a\n")
	if v := ToolVersionRev(repo, "v0.0.104", "1"); !strings.HasSuffix(v, ".dirty") {
		t.Errorf("an uncommitted code change gave the kno version %s, want the .dirty mark", v)
	}
}
