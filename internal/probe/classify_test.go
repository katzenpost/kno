package probe

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		obs  Observation
		want Verdict
	}{
		{"authority up and signing", Observation{
			Role: RoleAuthority, Up: true, ConsensusOK: true, Signing: true,
		}, Verdict{StatusOK, false}},
		{"authority up, no consensus fetched", Observation{
			Role: RoleAuthority, Up: true, ConsensusOK: false, Signing: false,
		}, Verdict{StatusOK, false}},
		{"authority up, no consensus fetched, signing irrelevant", Observation{
			Role: RoleAuthority, Up: true, ConsensusOK: false, Signing: true,
		}, Verdict{StatusOK, false}},
		{"authority up but not signing", Observation{
			Role: RoleAuthority, Up: true, ConsensusOK: true, Signing: false,
		}, Verdict{StatusNotSigning, true}},
		{"authority down, signing", Observation{
			Role: RoleAuthority, Up: false, ConsensusOK: true, Signing: true,
		}, Verdict{StatusDown, true}},
		{"authority down, not signing", Observation{
			Role: RoleAuthority, Up: false, ConsensusOK: true, Signing: false,
		}, Verdict{StatusDown, true}},
		{"authority down, no consensus fetched", Observation{
			Role: RoleAuthority, Up: false, ConsensusOK: false,
		}, Verdict{StatusDown, true}},
		{"authority ignores the orphan whitelist", Observation{
			Role: RoleAuthority, Up: true, ConsensusOK: true, Signing: false, KnownOrphan: true,
		}, Verdict{StatusNotSigning, true}},

		{"node up, no consensus fetched", Observation{
			Role: "mix", Up: true, ConsensusOK: false, ConfigAddr: "10.0.0.1:1",
		}, Verdict{StatusUp, false}},
		{"node down, no consensus fetched", Observation{
			Role: "mix", Up: false, ConsensusOK: false, ConfigAddr: "10.0.0.1:1",
		}, Verdict{StatusDown, true}},
		{"known orphan is still UP without a consensus", Observation{
			Role: "mix", Up: true, ConsensusOK: false, ConfigAddr: "10.0.0.1:1", KnownOrphan: true,
		}, Verdict{StatusUp, false}},

		{"node up, in consensus, same address", Observation{
			Role: "mix", Up: true, ConsensusOK: true,
			ConfigAddr: "10.0.0.1:1", ConsensusAddr: "10.0.0.1:1",
		}, Verdict{StatusOK, false}},
		{"node up, in consensus, different address", Observation{
			Role: "mix", Up: true, ConsensusOK: true,
			ConfigAddr: "10.0.0.1:1", ConsensusAddr: "10.0.0.2:2",
		}, Verdict{Mismatch("10.0.0.2:2"), true}},
		{"node up, absent from consensus", Observation{
			Role: "mix", Up: true, ConsensusOK: true, ConfigAddr: "10.0.0.1:1",
		}, Verdict{StatusOrphan, true}},
		{"node up, absent from consensus, whitelisted", Observation{
			Role: "mix", Up: true, ConsensusOK: true, ConfigAddr: "10.0.0.1:1", KnownOrphan: true,
		}, Verdict{StatusKnownOrphan, false}},
		{"node down, in consensus at the same address", Observation{
			Role: "mix", Up: false, ConsensusOK: true,
			ConfigAddr: "10.0.0.1:1", ConsensusAddr: "10.0.0.1:1",
		}, Verdict{StatusGhost, true}},
		{"node down, in consensus at another address", Observation{
			Role: "mix", Up: false, ConsensusOK: true,
			ConfigAddr: "10.0.0.1:1", ConsensusAddr: "10.0.0.2:2",
		}, Verdict{StatusGhost, true}},
		{"node down, absent from consensus", Observation{
			Role: "mix", Up: false, ConsensusOK: true, ConfigAddr: "10.0.0.1:1",
		}, Verdict{StatusDown, true}},
		{"whitelist does not excuse a down node", Observation{
			Role: "mix", Up: false, ConsensusOK: true, ConfigAddr: "10.0.0.1:1", KnownOrphan: true,
		}, Verdict{StatusDown, true}},
		{"whitelist does not excuse a ghost", Observation{
			Role: "mix", Up: false, ConsensusOK: true,
			ConfigAddr: "10.0.0.1:1", ConsensusAddr: "10.0.0.1:1", KnownOrphan: true,
		}, Verdict{StatusGhost, true}},
		{"whitelist does not excuse a mismatch", Observation{
			Role: "mix", Up: true, ConsensusOK: true,
			ConfigAddr: "10.0.0.1:1", ConsensusAddr: "10.0.0.2:2", KnownOrphan: true,
		}, Verdict{Mismatch("10.0.0.2:2"), true}},

		{"replica is classified like any other node", Observation{
			Role: "replica", Up: true, ConsensusOK: true,
			ConfigAddr: "10.0.0.1:1", ConsensusAddr: "10.0.0.1:1",
		}, Verdict{StatusOK, false}},
		{"gateway is classified like any other node", Observation{
			Role: "gateway", Up: true, ConsensusOK: true, ConfigAddr: "10.0.0.1:1",
		}, Verdict{StatusOrphan, true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Classify(c.obs); got != c.want {
				t.Fatalf("Classify(%+v) = %+v, want %+v", c.obs, got, c.want)
			}
		})
	}
}

func TestKnownOrphans(t *testing.T) {
	known := KnownOrphans(" wauland2  storagereplica3 ")
	if len(known) != 2 {
		t.Fatalf("KnownOrphans = %v, want 2 tokens", known)
	}
	for _, c := range []struct {
		id   string
		want bool
	}{
		{"wauland2", true},
		{"storagereplica3", true},
		{"wauland", false},
		{"wauland22", false},
		{"storagereplica", false},
		{"", false},
	} {
		if got := IsKnownOrphan(known, c.id); got != c.want {
			t.Errorf("IsKnownOrphan(%q) = %v, want %v", c.id, got, c.want)
		}
	}

	if IsKnownOrphan(KnownOrphans("none"), "wauland2") {
		t.Error(`"none" must not whitelist every identifier`)
	}
	if len(KnownOrphans("")) != 0 {
		t.Error("an unset whitelist must be empty")
	}
}

func TestUnknownInConsensus(t *testing.T) {
	eps := []Endpoint{
		{Role: "mix", Identifier: "wauland", Host: "10.0.0.1", Port: "1"},
		{Role: RoleAuthority, Identifier: "wauland", Host: "10.0.0.1", Port: "2"},
		{Role: "replica", Identifier: "storagereplica0", Host: "10.0.0.2", Port: "3"},
	}
	nodes := []ConsensusNode{
		{Identifier: "wauland", Host: "10.0.0.1", Port: "1"},
		{Identifier: "storagereplica0", Host: "10.0.0.2", Port: "3"},
		{Identifier: "ghostmix", Host: "10.0.0.9", Port: "9"},
	}
	got := UnknownInConsensus(nodes, eps)
	if len(got) != 1 || got[0].Identifier != "ghostmix" || got[0].Addr() != "10.0.0.9:9" {
		t.Fatalf("UnknownInConsensus = %+v, want only ghostmix at 10.0.0.9:9", got)
	}
	if out := UnknownInConsensus(nil, eps); out != nil {
		t.Fatalf("empty consensus should report nothing, got %+v", out)
	}
}

func TestRoleOf(t *testing.T) {
	for base, want := range map[string]string{
		"luscus-pq-authority":       RoleAuthority,
		"wauland-pq-mixserver":      "mix",
		"wauland-pq-gatewaynode":    "gateway",
		"wauland-pq-servicenode":    "service",
		"wauland-pq-storagenode":    "replica",
		"annares-pq-storagenode-0":  "replica",
		"windfall-pq-storagenode-2": "replica",
		"courier":                   "node",
		"somehost-pq-somethingelse": "node",
	} {
		if got := RoleOf(base); got != want {
			t.Errorf("RoleOf(%q) = %q, want %q", base, got, want)
		}
	}
}
