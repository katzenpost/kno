package preflight

import (
	"os/exec"
	"strconv"
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
	if _, err := exec.LookPath("df"); err != nil {
		t.Skip("no df")
	}
	dir := t.TempDir()
	out, err := exec.Command("sh", "-c", Probe(map[string]string{"x": dir}, nil)).Output()
	if err != nil {
		t.Fatal(err)
	}
	want, err := exec.Command("sh", "-c", "df -Pk "+dir+" | awk 'NR==2{print $4}'").Output()
	if err != nil {
		t.Fatal(err)
	}
	got, err1 := strconv.ParseInt(Parse(string(out))["disk"], 10, 64)
	free, err2 := strconv.ParseInt(strings.TrimSpace(string(want)), 10, 64)
	if err1 != nil || err2 != nil || got <= 0 || got-free > 102400 || free-got > 102400 {
		t.Errorf("disk = %d KB, want about the free space of %s (%d KB)", got, dir, free)
	}
}
