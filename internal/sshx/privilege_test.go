package sshx

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSuRemoteActivationNeedsNoSudoAndKeepsArgumentsLiteral(t *testing.T) {
	dir := t.TempDir()
	for name, script := range map[string]string{
		"id":   "printf '1000\\n'",
		"su":   "[ \"$1\" = root ] && [ \"$2\" = -c ] || exit 91; exec sh -c \"$3\"",
		"sudo": "exit 92",
		"kno":  "printf '%s\\n' \"$@\"",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	arg := "release with 'quotes' and $(exit 93)"
	command := AsRootWith("su", "kno activate "+Quote(arg)+" v0.0.107")
	out, err := exec.Command("sh", "-ec", command).CombinedOutput()
	if err != nil || string(out) != "activate\n"+arg+"\nv0.0.107\n" {
		t.Fatalf("remote su activation: %q (%v)", out, err)
	}
	argv := SSHForRoot("ssh", "admin@windfall1", "su", command)
	if !slices.Contains(argv, "-tt") || strings.Contains(command, "sudo") {
		t.Fatalf("su lacked a terminal or still used sudo: %v", argv)
	}
}
