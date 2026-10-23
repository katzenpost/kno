package status

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/knopath"
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
	if !strings.Contains(out.String(), "kpauthority/katzenpost") || !strings.Contains(out.String(), " active") {
		t.Errorf("the jailed service has no state:\n%s%s", out.String(), errOut.String())
	}
	if strings.Contains(out.String(), "systemctl not present") {
		t.Errorf("status says systemctl is missing on an rc.d host:\n%s", out.String())
	}
	if len(fake.asked) != 1 || fake.asked[0] != "kpauthority/katzenpost" {
		t.Errorf("asked %v, want kpauthority/katzenpost", fake.asked)
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

func TestReachabilityProbesEachAnnouncedAddressAndExplainsAHairpin(t *testing.T) {
	open, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer open.Close()
	shut, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := shut.Addr().String()
	shut.Close()
	repo := t.TempDir()
	gc := knopath.ConfigsDir(repo, "n")
	if err := os.MkdirAll(gc, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[Server]\n  Addresses = [\"tcp://" + closed + "\", \"tcp://" + open.Addr().String() + "\"]\n" +
		"  BindAddresses = [\"tcp://" + open.Addr().String() + "\"]\n"
	if err := os.WriteFile(filepath.Join(gc, "x-pq-gatewaynode.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	writeReachability(&out, track.Env{Repo: repo, Network: "n", Services: []string{"x-pq-gatewaynode"}})
	got := strings.Join(strings.Fields(out.String()), " ")
	if !strings.Contains(got, closed+" no answer from this machine, but the service answers on "+open.Addr().String()) ||
		!strings.Contains(got, open.Addr().String()+" reachable") {
		t.Errorf("reachability:\n%s", got)
	}
}

func TestAHostThatUpdatesItselfShowsItsVerifyWindow(t *testing.T) {
	dir := t.TempDir()
	for f, body := range map[string]string{
		"state":   "pending\n",
		"current": "v0.0.107\n",
		"pending": "tag=v0.0.107\nactivated=1792780560\npromote=1792780560\ndeadline=1792785960\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e := track.Env{StateDir: dir, Config: config.Config{Hosts: map[string]config.Host{"pietje": {}}}}
	mark, detail := hostLine(e, "pietje", "v0.0.107", nil)
	if mark != "VERIFY" || !strings.Contains(detail, "verifying v0.0.107") || !strings.Contains(detail, "window closes") {
		t.Errorf("hostLine = %s %q, want VERIFY with the window", mark, detail)
	}
	if got := hostStateDir(track.Env{StateDir: dir, Config: config.Config{Hosts: map[string]config.Host{"annares": {SSH: "root@annares"}}}}, "annares"); got != knopath.HostState(dir, "annares") {
		t.Errorf("a remote host's state dir = %s", got)
	}
}
