package hostsetup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootstrapSaysWhyItCannotRunWithoutUseradd(t *testing.T) {
	none := func(string) (string, error) { return "", errors.New("not found") }
	err := bootstrapSupported(none)
	if err == nil || !strings.Contains(err.Error(), "FreeBSD") || !strings.Contains(err.Error(), "useradd") {
		t.Errorf("err = %v, want the reason and what a FreeBSD node does instead", err)
	}
	found := func(string) (string, error) { return "/usr/sbin/useradd", nil }
	if err := bootstrapSupported(found); err != nil {
		t.Errorf("a Debian host was refused: %v", err)
	}
}

func TestDepsReadsItsVariables(t *testing.T) {
	for _, m := range []string{ManagerApt, ManagerPkg} {
		name := "DEPS_DEBIAN_BUILD"
		if m == ManagerPkg {
			name = "DEPS_FREEBSD_BUILD"
		}
		t.Setenv(name, "marker-package")
		if got := EnvPackages(m).Build; len(got) != 1 || got[0] != "marker-package" {
			t.Fatalf("%s does not set the build group: %v", name, got)
		}
	}
}

func TestErrorsNameTheCommandThatFailed(t *testing.T) {
	cmd := DepsCommand()
	cmd.SetArgs([]string{"--manager", "yum"})
	var errOut strings.Builder
	cmd.SetOut(&errOut)
	cmd.SetErr(&errOut)
	if err := cmd.Execute(); err == nil {
		t.Fatal("deps accepted an unknown manager")
	}
	if !strings.HasPrefix(errOut.String(), "kno "+cmd.Name()+": ") {
		t.Errorf("the error does not name 'kno %s': %q", cmd.Name(), errOut.String())
	}

	if os.Geteuid() == 0 {
		t.Skip("as root, a bootstrap that passed --rev would change this host")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "useradd"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "networks"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KNO_REPO", repo)
	boot := BootstrapCommand()
	boot.SetArgs([]string{"--rev", "0123abcd"})
	errOut.Reset()
	boot.SetOut(&errOut)
	boot.SetErr(&errOut)
	if err := boot.Execute(); err == nil {
		t.Fatal("bootstrap --rev passed outside a git checkout")
	}
	if !strings.HasPrefix(errOut.String(), "kno "+boot.Name()+": ") {
		t.Errorf("the error does not name 'kno %s': %q", boot.Name(), errOut.String())
	}
}
