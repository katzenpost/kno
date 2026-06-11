package config

import (
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

func TestClaimedHostsHonoursTheClaim(t *testing.T) {
	repo := testutil.RepoRoot(t)
	c := Config{
		Network: "namenlos",
		Nodes:   []string{"mixy", "reed"},
		Hosts: map[string]Host{
			"mixy":       {SSH: "root@mixy", Services: []string{"mixy-pq-mixserver"}},
			"reed":       {SSH: "root@reed", Services: []string{"reed-pq-mixserver"}},
			"oneeyedman": {SSH: "root@oneeyedman", Services: []string{"oneeyedman-pq-mixserver"}},
		},
	}
	got := ClaimedHosts(repo, c)
	if len(got) != 2 || got[0] != "mixy" || got[1] != "reed" {
		t.Errorf("ClaimedHosts = %v, want [mixy reed]", got)
	}

	if un := UnclaimedHosts(repo, c); len(un) != 1 || un[0] != "oneeyedman" {
		t.Errorf("UnclaimedHosts = %v, want [oneeyedman]", un)
	}
}

func TestAnEmptyClaimMeansEveryHost(t *testing.T) {
	repo := testutil.RepoRoot(t)
	c := Config{
		Network: "namenlos",
		Hosts: map[string]Host{
			"mixy": {SSH: "root@mixy", Services: []string{"mixy-pq-mixserver"}},
			"reed": {SSH: "root@reed", Services: []string{"reed-pq-mixserver"}},
		},
	}
	if got := ClaimedHosts(repo, c); len(got) != 2 {
		t.Errorf("ClaimedHosts = %v, want both hosts", got)
	}
	if un := UnclaimedHosts(repo, c); len(un) != 0 {
		t.Errorf("UnclaimedHosts = %v, want none", un)
	}
}

func TestAHostIsClaimedByItsServicesNotItsLabel(t *testing.T) {
	repo := testutil.RepoRoot(t)
	c := Config{
		Network: "namenlos",
		Nodes:   []string{"mixy"},
		Hosts: map[string]Host{
			"someotherbox": {SSH: "root@box", Services: []string{"mixy-pq-mixserver"}},
		},
	}
	if got := ClaimedHosts(repo, c); len(got) != 1 || got[0] != "someotherbox" {
		t.Errorf("ClaimedHosts = %v, want [someotherbox]", got)
	}
}

func TestSummaryIsTheConfigAndNotTheVersions(t *testing.T) {
	var b strings.Builder
	Summary(&b, "/etc/katzenpost/kno.toml", Config{
		Network: "namenlos",
		Nodes:   []string{"mixy"},
		Hosts:   map[string]Host{"mixy": {SSH: "root@mixy", Services: []string{"mixy-pq-mixserver"}}},
	}, testutil.RepoRoot(t))
	got := b.String()
	for _, want := range []string{"network: namenlos", "nodes:   mixy", "root@mixy"} {
		if !strings.Contains(got, want) {
			t.Errorf("Summary omits %q:\n%s", want, got)
		}
	}

	for _, never := range []string{"0.0.10", "target", "last-good", "kno status"} {
		if strings.Contains(got, never) {
			t.Errorf("Summary mentions %q, which is status's: \n%s", never, got)
		}
	}
}
