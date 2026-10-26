package status

import (
	"strings"
	"testing"
)

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 && r != '\n' || r == 0x7f || r == 0x9b {
			return true
		}
	}
	return false
}

func TestAHostReplyCannotPutControlBytesOnTheTerminal(t *testing.T) {
	esc := "\x1b]52;c;cm0gLXJmIH4K\x07\x1b[2K"
	got := parseRunning("kpserver@x.service " + esc + " 1.0 current\n")
	for u, v := range got {
		if hasControl(v) {
			t.Errorf("running info for %s carries control bytes from the host: %q", u, v)
		}
	}
	if r := sshReason(esc + "permission denied"); hasControl(r) {
		t.Errorf("the ssh reason carries control bytes from the host: %q", r)
	}
	for _, s := range parseStaged("x" + esc + ".deb\n") {
		if hasControl(s) || strings.ContainsRune(s, 0x1b) {
			t.Errorf("a staged name carries control bytes from the host: %q", s)
		}
	}
}
