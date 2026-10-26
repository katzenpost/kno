package knopath

import (
	"os/user"
	"testing"
)

func TestOnlyRootUnderSudoUsesTheInvokersKnoDirectory(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Skip(err)
	}
	for _, c := range []struct {
		euid int
		name string
	}{{1000, me.Username}, {0, ""}, {0, "root"}, {0, "no-such-user-kno"}} {
		if got := invokerArtifacts(c.euid, c.name); got != "" {
			t.Errorf("euid %d, SUDO_USER %q: %s", c.euid, c.name, got)
		}
	}
}
