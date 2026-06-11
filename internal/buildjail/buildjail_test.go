package buildjail

import (
	"strings"
	"testing"
)

func TestOnlyGitAndGoRunInTheBuildJail(t *testing.T) {
	if _, ok := Dispatch([]string{"/usr/local/bin/kno", "update"}); ok {
		t.Error("kno itself is sent to the build jail")
	}
	t.Setenv("KNO_BUILD_JAIL", "")
	if code, ok := Dispatch([]string{"/var/jail/kno/data/libexec/git", "status"}); !ok || code != 127 {
		t.Errorf("git without a build jail gave %d, %v; want 127, true", code, ok)
	}
}

func TestTheToolRunsInTheSameDirectoryWithTheSameEnvironment(t *testing.T) {
	got := jexecArgs("mixbuilder", "/var/jail/kno/data/network-organizer", "git",
		[]string{"PATH=/usr/bin", "HOME=/var/jail/kno/data", "GIT_TERMINAL_PROMPT=0"},
		[]string{"-C", "/var/jail/kno/data/network-organizer", "log", "--format=%h %s"})
	want := []string{"mixbuilder", "/usr/bin/env", "-i", "HOME=/var/jail/kno/data", "GIT_TERMINAL_PROMPT=0",
		"PATH=" + jailPATH, "/bin/sh", "-c", `cd "$0" 2>/dev/null || cd /; exec "$@"`,
		"/var/jail/kno/data/network-organizer", "git",
		"-C", "/var/jail/kno/data/network-organizer", "log", "--format=%h %s"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("jexec arguments\n got %q\nwant %q", got, want)
	}
}
