package schedule

import (
	"slices"
	"strings"
	"testing"
)

func TestSuSchedulesLocallyAndRemotelyWithoutSudo(t *testing.T) {
	local := opts(t, ModeTimer, "")
	local.Sudo = "su"
	p := plan(t, local)
	argv := p.Steps[0].Argv
	if len(p.Steps) != 1 || argv[0] != "su" || argv[1] != "root" || argv[2] != "-c" || !strings.Contains(argv[3], "--schedule=timer") {
		t.Fatalf("local su scheduler: %v", p.Commands())
	}
	remote := opts(t, ModeTimer, "admin@windfall1")
	remote.Privilege = "su"
	p = plan(t, remote)
	argv = p.Steps[0].Argv
	if !slices.Contains(argv, "-tt") || !strings.Contains(argv[len(argv)-1], "su root -c") || strings.Contains(argv[len(argv)-1], "sudo") {
		t.Fatalf("remote su scheduler: %v", p.Commands())
	}
}
