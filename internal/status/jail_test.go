package status

import (
	"os"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/sshx"
	"github.com/katzenpost/kno/internal/testutil"
	"github.com/katzenpost/kno/internal/track"
	"github.com/katzenpost/kno/internal/verify"
)

type rcFake struct{ asked []string }

func (*rcFake) Available() bool { return true }

func (f *rcFake) State(unit string) verify.UnitState {
	f.asked = append(f.asked, unit)
	return verify.UnitState{Unit: unit, Active: "active"}
}

func TestStatusAsksAJailedServiceInItsJail(t *testing.T) {
	fake := &rcFake{}
	old := unitStates
	unitStates = func() verify.Systemd { return fake }
	t.Cleanup(func() { unitStates = old })

	var out, errOut strings.Builder
	e := track.Env{Repo: testutil.RepoRoot(t), Network: "namenlos", Services: []string{"respectmy-pq-authority"}}
	writeServices(&out, &errOut, e, false, false)
	if !strings.Contains(out.String(), "respectmy/katzenpost") || !strings.Contains(out.String(), " active") {
		t.Errorf("the jailed service has no state:\n%s%s", out.String(), errOut.String())
	}
	if strings.Contains(out.String(), "systemctl not present") {
		t.Errorf("status says systemctl is missing on an rc.d host:\n%s", out.String())
	}
	if len(fake.asked) != 1 || fake.asked[0] != "respectmy/katzenpost" {
		t.Errorf("asked %v, want respectmy/katzenpost", fake.asked)
	}
}

func TestAJailHostAnswersWithItsRcDStates(t *testing.T) {
	script := sshx.CLocale(hostQuery([]string{"respectmy/katzenpost", "allmixedup/katzenpost"}))
	out, err := testutil.RemoteShell(t, 0, script, "service", "ls", "sed", "sort")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	head, rest, _ := strings.Cut(out, identMark)
	if got := strings.Fields(head); len(got) != 2 || got[0] != "active" || got[1] != "active" {
		t.Errorf("unit states = %q, want active active from service -j", got)
	}
	tail, _, _ := strings.Cut(rest, stagedMark)
	id := parseIdent(tail)
	if _, err := os.Stat("/etc/os-release"); err == nil && (id.Arch != "" || id.OS == "") {
		t.Errorf("a host without dpkg gave ident %+v from %q; want no arch and the OS", id, tail)
	}
	if got := parseStaged("katzenpost-0.0.107.pkg\nkno_0.0.107-1_amd64.deb\nnotes.txt\n"); len(got) != 2 {
		t.Errorf("staged = %q, want the .pkg and the .deb", got)
	}
}
