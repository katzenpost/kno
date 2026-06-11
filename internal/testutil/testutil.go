package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func RepoRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(ModuleRoot(t), "data")
}

func ModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find the module root (no go.mod above the test)")
		}
		dir = parent
	}
}

func TreeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		mark := ""
		if fi.Mode().Perm()&0o100 != 0 {
			mark = "\x00exec"
		}
		out[rel] = string(b) + mark
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}

func RemoteShell(t *testing.T, uid int, script string, tools ...string) (string, error) {
	t.Helper()
	bin := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env, err := exec.LookPath("env")
	if err != nil {
		t.Fatal(err)
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]string{"env": env, "sh": sh} {
		if err := os.Symlink(p, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	write("id", fmt.Sprintf("echo %d", uid))
	if uid != 0 {
		tools = append(tools, "sudo")
	}
	for _, tool := range tools {
		write(tool, `echo "`+tool+` $*"`)
	}
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = []string{"PATH=" + bin}
	out, err := cmd.CombinedOutput()
	return string(out), err
}
