package preflight

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheRunningTagComesFromTheKatzenpostVersion(t *testing.T) {
	for in, want := range map[string]string{
		"0.0.104+ubuntu26.04": "v0.0.104", "0.0.107": "v0.0.107", "0.0.104-259": "v0.0.104", "": "",
	} {
		if got := runningTag(in); got != want {
			t.Errorf("runningTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAnUpstreamKatzenpostStaysAtTheBuildForItsOS(t *testing.T) {
	var r Report
	judgeVersions(&r, Input{State: state("pkgs", "katzenpost=0.0.107;"),
		Packages: map[string][]string{"katzenpost": {"0.0.107+ubuntu26.04"}}})
	got := findings(r, "version")
	if len(got) != 1 || !strings.Contains(got[0].Detail, "stays at 0.0.107") {
		t.Errorf("katzenpost 0.0.107 against the build 0.0.107+ubuntu26.04 gave %+v, want it to stay", r.Findings)
	}
}

func TestTheDiskCheckReadsTheStateFilesystem(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state filesystem")
	bin := t.TempDir()
	t.Setenv("KNO_TEST_STATE_DIR", dir)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := `#!/bin/sh
[ "$#" = 2 ] && [ "$1" = -Pk ] && [ "$2" = "$KNO_TEST_STATE_DIR" ] || exit 1
printf 'Filesystem 1024-blocks Used Available Capacity Mounted on\nstate 10000 5758 4242 58%% /state\n'
`
	if err := os.WriteFile(filepath.Join(bin, "df"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", "-c", Probe(map[string]string{"x": dir}, nil)).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := Parse(string(out))["disk"]; got != "4242" {
		t.Errorf("disk = %q, want the state filesystem's 4242 KB", got)
	}
}
