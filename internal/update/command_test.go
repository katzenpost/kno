package update

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/track"
)

func TestPushOnlyMachineHasNothingToUpdate(t *testing.T) {
	cases := []struct {
		name string
		env  track.Env
		want bool
	}{
		{"operator machine", track.Env{Managed: []track.ManagedService{
			{Host: "mixy", SSH: "root@mixy", Service: "mixy-pq-mixserver"},
			{Host: "reed", SSH: "root@reed", Service: "reed-pq-mixserver"},
		}}, true},
		{"node", track.Env{
			Services: []string{"mixy-pq-mixserver"},
			Managed:  []track.ManagedService{{Host: "mixy", Service: "mixy-pq-mixserver"}},
		}, false},

		{"no config", track.Env{Services: []string{"mixy-pq-mixserver"}}, false},

		{"empty", track.Env{}, false},
	}
	for _, c := range cases {
		if got := pushOnly(c.env); got != c.want {
			t.Errorf("%s: pushOnly = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestEachOrganizedHostGetsItsOwnState(t *testing.T) {
	e := track.Env{StateDir: "/var/state"}
	a := hostEnv(e, remoteHost{Label: "mixy", Services: []string{"mixy-pq-mixserver"}})
	b := hostEnv(e, remoteHost{Label: "reed", Services: []string{"reed-pq-mixserver"}})

	if a.StateDir == b.StateDir {
		t.Fatalf("two hosts share one state dir: %s", a.StateDir)
	}
	if a.StateDir == e.StateDir {
		t.Errorf("a host's state dir is the machine's own: %s", a.StateDir)
	}
	for _, c := range []struct {
		what, got, want string
	}{
		{"state dir", a.StateDir, filepath.Join("/var/state", "hosts", "mixy")},
		{"current", a.CurrentFile, filepath.Join("/var/state", "hosts", "mixy", "current")},
		{"quarantine", a.QuarantineFile, filepath.Join("/var/state", "hosts", "mixy", "quarantine")},
	} {
		if c.got != c.want {
			t.Errorf("%s = %s, want %s", c.what, c.got, c.want)
		}
	}
	if len(a.Services) != 1 || a.Services[0] != "mixy-pq-mixserver" {
		t.Errorf("the host's tick does not run against its own services: %v", a.Services)
	}
}

func TestOrganizedHostsAreEveryHostThatCarriesAService(t *testing.T) {
	repo := t.TempDir()
	e := track.Env{Repo: repo, Config: config.Config{Hosts: map[string]config.Host{
		"reed":  {SSH: "root@reed", Services: []string{"reed-pq-mixserver"}},
		"mixy":  {SSH: "root@mixy", Services: []string{"mixy-pq-mixserver"}},
		"spare": {SSH: "root@spare"},
	}}}
	got := organizedHosts(e)
	if len(got) != 2 {
		t.Fatalf("got %d host(s), want the 2 that carry services: %+v", len(got), got)
	}
	if got[0].Label != "mixy" || got[1].Label != "reed" {
		t.Errorf("hosts are not in a stable order: %s %s", got[0].Label, got[1].Label)
	}
	if got[0].Node != "root@mixy" {
		t.Errorf("ssh target not carried through: %s", got[0].Node)
	}
}

func TestAHostWithNoSSHTargetIsAddressedByLabel(t *testing.T) {
	e := track.Env{Repo: t.TempDir(), Config: config.Config{Hosts: map[string]config.Host{
		"mixy": {Services: []string{"mixy-pq-mixserver"}},
	}}}
	got := organizedHosts(e)
	if len(got) != 1 || got[0].Node != "mixy" {
		t.Fatalf("got %+v, want the label as the node", got)
	}
}

func TestThePolicyOfTheRunDecidesOnTheOrganizer(t *testing.T) {
	t.Setenv("KNO_TRACK_FETCH", "0")
	e := track.Env{Repo: t.TempDir(), StateDir: t.TempDir(), Config: config.Config{Hosts: map[string]config.Host{
		"mixy": {Services: []string{"mixy-pq-mixserver"}},
	}}}
	var b bytes.Buffer
	if rc := runRemote(&b, e, Flags{Policy: "off"}); rc != 0 || !strings.Contains(b.String(), "'off'") {
		t.Errorf("--policy off: rc = %d\n%s", rc, b.String())
	}
	for _, c := range []struct {
		env, flag string
		apply     bool
		dry       bool
	}{
		{"off", "", false, false},
		{"off", "apply", true, false},
		{"", "simulate", true, true},
		{"simulate", "", true, true},
		{"apply", "", true, false},
	} {
		e.UpdatePolicy = c.env
		v, apply := remotePolicy(e, Flags{Policy: c.flag})
		if apply != c.apply || v.Dry != c.dry {
			t.Errorf("policy %q, --policy %q: apply = %v, dry = %v; want %v, %v",
				c.env, c.flag, apply, v.Dry, c.apply, c.dry)
		}
	}
}
