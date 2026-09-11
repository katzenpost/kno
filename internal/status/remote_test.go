package status

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/track"
)

func fakeSSH(t *testing.T, code int, say string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ssh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nprintf '%s' \""+say+"\"\nexit "+
		strconv.Itoa(code)+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAHostsUnitStatesComeBackInOrder(t *testing.T) {
	e := track.Env{Repo: t.TempDir(), Network: "namenlos"}
	svcs := []string{"annares-pq-authority", "annares-pq-servicenode", "annares-pq-storagenode-0"}
	ssh := fakeSSH(t, 3, "active\nfailed\ninactive\n")

	got, _, _, why := askHost(context.Background(), ssh, "root@annares", "", e, svcs)
	if why != "" {
		t.Errorf("a host that answered for every unit reported a problem: %s", why)
	}
	want := map[string]string{
		"annares-pq-authority":     "active",
		"annares-pq-servicenode":   "failed",
		"annares-pq-storagenode-0": "inactive",
	}
	for svc, w := range want {
		if got[svc] != w {
			t.Errorf("%s = %q, want %q", svc, got[svc], w)
		}
	}
}

func TestSSHNoiseIsNotAUnitState(t *testing.T) {
	e := track.Env{Repo: t.TempDir(), Network: "namenlos"}
	ssh := fakeSSH(t, 255, "Permission denied (publickey).\n")

	got, _, _, why := askHost(context.Background(), ssh, "root@mixy", "", e, []string{"mixy-pq-mixserver"})
	if got["mixy-pq-mixserver"] != unknown {
		t.Errorf("got %q, want %q", got["mixy-pq-mixserver"], unknown)
	}

	if !strings.Contains(why, "Permission denied") {
		t.Errorf("the reason was dropped: %q", why)
	}
}

func TestAShortAnswerDoesNotShiftTheStates(t *testing.T) {
	e := track.Env{Repo: t.TempDir(), Network: "namenlos"}
	ssh := fakeSSH(t, 0, "active\n")

	got, _, _, _ := askHost(context.Background(), ssh, "root@mixy", "", e,
		[]string{"mixy-pq-mixserver", "reed-pq-mixserver"})
	if got["mixy-pq-mixserver"] != "active" {
		t.Errorf("first = %q, want active", got["mixy-pq-mixserver"])
	}
	if got["reed-pq-mixserver"] != unknown {
		t.Errorf("second = %q, want %q", got["reed-pq-mixserver"], unknown)
	}
}

func TestStatusCanBeToldNotToSSH(t *testing.T) {
	t.Setenv("KNO_STATUS_SSH", "0")
	e := track.Env{Managed: []track.ManagedService{
		{Host: "mixy", SSH: "root@mixy", Service: "mixy-pq-mixserver"},
	}}
	if got, _, _, _ := askHosts(e); len(got) != 0 {
		t.Errorf("status reached for the network anyway: %v", got)
	}
}

func TestStatusSaysWhichVersionEachUnitRuns(t *testing.T) {
	out := "kpserver@reed-pq-mixserver katzenpost 0.0.107 current\n" +
		"kpclientd.service katzenpost 0.0.104+ubuntu24.04 replaced\n" +
		"pq-katzenpost-mixserver - - current\n" +
		"kno-self kno 0.0.107-1+20261021000047.gbe621db current\n"
	got := parseRunning(out)
	if got["kpserver@reed-pq-mixserver"] != "katzenpost 0.0.107" {
		t.Errorf("kpserver = %q", got["kpserver@reed-pq-mixserver"])
	}
	if !strings.Contains(got["kpclientd.service"], "restart needed") {
		t.Errorf("a unit that runs a replaced binary does not say restart: %q", got["kpclientd.service"])
	}
	if _, ok := got["pq-katzenpost-mixserver"]; ok {
		t.Error("a unit with no package was reported")
	}
	if got["kno-self"] != "kno 0.0.107-1+20261021000047.gbe621db" {
		t.Errorf("kno = %q", got["kno-self"])
	}
	cmd := runningCmd([]string{"kpserver@reed-pq-mixserver", "jail/katzenpost"})
	if !strings.Contains(cmd, "MainPID --value kpserver@reed-pq-mixserver") || strings.Contains(cmd, "jail/katzenpost") {
		t.Errorf("runningCmd = %s", cmd)
	}
}

func TestARememberedStateCountsOnlyForTheUnitStatusAsksFor(t *testing.T) {
	t.Setenv("KNO_ARTIFACTS", t.TempDir())
	p := knopath.Observed("annares")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "annares-pq-authority kpauthority@annares-pq-authority inactive\n" +
		"annares-pq-servicenode pq-katzenpost-mixserver active\n" +
		"annares-pq-storagenode-0 inactive\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	legacy := map[string]string{
		"annares-pq-authority":     "pq-katzenpost-authority",
		"annares-pq-servicenode":   "pq-katzenpost-mixserver",
		"annares-pq-storagenode-0": "pq-katzenpost-storage-replica-0",
	}
	got := ObservedStates("annares", func(svc string) string { return legacy[svc] })
	if _, ok := got["annares-pq-authority"]; ok {
		t.Error("a state remembered for kpauthority@ counted for a host that runs pq-katzenpost-authority")
	}
	if got["annares-pq-servicenode"] != "active" {
		t.Errorf("servicenode = %q, want the state remembered for its own unit", got["annares-pq-servicenode"])
	}
	if _, ok := got["annares-pq-storagenode-0"]; ok {
		t.Error("an old line with no unit counted")
	}
}
