package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSuKeepsCommandArgumentsLiteral(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "su")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\n[ \"$1\" = root ] && [ \"$2\" = -c ] || exit 99\nexec sh -c \"$3\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(dir, "command with spaces")
	if err := os.WriteFile(program, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	args := []string{"a space", "single'quote", "$(exit 42)", "; exit 43", ""}
	argv := Elevate(helper, append([]string{program}, args...)...)
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	if err != nil || string(out) != strings.Join(args, "\n")+"\n" {
		t.Fatalf("su changed arguments or executed argument content: %q (%v)", out, err)
	}
}

func TestElevationRetainsExistingSudoAndDoasArguments(t *testing.T) {
	for helper, want := range map[string][]string{
		"":        {"kno", "activate", "release", "v1"},
		"sudo":    {"sudo", "kno", "activate", "release", "v1"},
		"doas -n": {"doas", "-n", "kno", "activate", "release", "v1"},
	} {
		if got := Elevate(helper, "kno", "activate", "release", "v1"); !slices.Equal(got, want) {
			t.Fatalf("%q: %v, want %v", helper, got, want)
		}
	}
}

func TestExplicitRootHelperOverrideIsKept(t *testing.T) {
	t.Setenv("SUDO", "custom-helper")
	if got := RootHelper("su"); got != "custom-helper" {
		t.Fatalf("explicit override changed: %q", got)
	}
}
