package compile

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fakeSource(t *testing.T, cmds ...string) string {
	t.Helper()
	work := t.TempDir()
	kp := filepath.Join(work, "katzenpost")
	for _, c := range cmds {
		dir := filepath.Join(kp, "cmd", c)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		src := "package main\n\nfunc main() { println(\"" + c + "\") }\n"
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(kp, "go.mod"), []byte("module katzenpost\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "go.work"), []byte("go 1.21\n\nuse ./katzenpost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return work
}

func TestToolchainBuildsBothConfigTools(t *testing.T) {
	work := fakeSource(t, ToolchainCmds...)
	bindir := filepath.Join(t.TempDir(), "bin")
	got, err := Toolchain(work, bindir)
	if err != nil {
		t.Fatalf("Toolchain: %v", err)
	}
	if got != bindir {
		t.Errorf("Toolchain returned %s, want %s", got, bindir)
	}
	for _, c := range ToolchainCmds {
		if _, err := os.Stat(filepath.Join(bindir, c)); err != nil {
			t.Errorf("no %s: %v", c, err)
		}
	}
}

func TestToolchainFailsOnAMissingTool(t *testing.T) {
	work := fakeSource(t, "genconfig")
	_, err := Toolchain(work, filepath.Join(t.TempDir(), "bin"))
	if err == nil {
		t.Fatal("Toolchain accepted a source tree with no kpconfig")
	}
	if !strings.Contains(err.Error(), "kpconfig") {
		t.Errorf("the failure does not name the missing tool: %v", err)
	}
}

func TestToolchainDefaultsTheBindirIntoTheWorkdir(t *testing.T) {
	work := fakeSource(t, ToolchainCmds...)
	got, err := Toolchain(work, "")
	if err != nil {
		t.Fatalf("Toolchain: %v", err)
	}
	if want := filepath.Join(work, "bin"); got != want {
		t.Errorf("default bindir = %s, want %s", got, want)
	}
	if _, err := os.Stat(filepath.Join(got, "genconfig")); err != nil {
		t.Errorf("nothing in the default bindir: %v", err)
	}
}

func TestToolchainTakesARelativeBindir(t *testing.T) {
	work := fakeSource(t, ToolchainCmds...)
	dir := t.TempDir()
	t.Chdir(dir)
	got, err := Toolchain(work, "out")
	if err != nil {
		t.Fatalf("Toolchain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out", "genconfig")); err != nil {
		t.Errorf("a relative bindir did not resolve against the caller's directory (got %s): %v", got, err)
	}
}

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

func fakeRepo(t *testing.T) string {
	t.Helper()
	base := t.TempDir()

	kp := filepath.Join(base, "katzenpost.git")
	src := fakeSource(t, ToolchainCmds...)
	if err := os.CopyFS(kp, os.DirFS(filepath.Join(src, "katzenpost"))); err != nil {
		t.Fatal(err)
	}
	git(t, kp, "init", "-q")
	git(t, kp, "add", "-A")
	git(t, kp, "commit", "-qm", "base")
	git(t, kp, "tag", "v1")

	hpqc := filepath.Join(base, "hpqc.git")
	if err := os.MkdirAll(hpqc, 0o755); err != nil {
		t.Fatal(err)
	}
	mod := "module github.com/katzenpost/hpqc\n\ngo 1.21\n"
	if err := os.WriteFile(filepath.Join(hpqc, "go.mod"), []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, hpqc, "init", "-q")
	git(t, hpqc, "add", "-A")
	git(t, hpqc, "commit", "-qm", "base")
	git(t, hpqc, "tag", "v1")

	repo := filepath.Join(base, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "thirdparty", "patches"), 0o755); err != nil {
		t.Fatal(err)
	}
	pins := strings.Join([]string{
		"GO_VERSION=1.21",
		"KATZENPOST_URL=file://" + kp,
		"KATZENPOST_REF=v1",
		"KATZENPOST_SRC=" + kp,
		"HPQC_URL=file://" + hpqc,
		"HPQC_REF=v1",
		"HPQC_SRC=" + hpqc,
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(repo, "thirdparty", "pins.env"), []byte(pins), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestBuildToolchainMaterializesThenBuilds(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := fakeRepo(t)
	work := filepath.Join(t.TempDir(), "work")
	bindir, err := BuildToolchain(repo, work, "")
	if err != nil {
		t.Fatalf("BuildToolchain: %v", err)
	}

	if want := filepath.Join(work, "bin"); bindir != want {
		t.Errorf("bindir = %s, want %s", bindir, want)
	}
	for _, c := range ToolchainCmds {
		if _, err := os.Stat(filepath.Join(bindir, c)); err != nil {
			t.Errorf("no %s: %v", c, err)
		}
	}

	if _, err := os.Stat(filepath.Join(work, "go.work")); err != nil {
		t.Errorf("no go.work in the workdir: %v", err)
	}
}
