package config

import (
	"bytes"
	"strings"
	"testing"
)

func TestAHostThatClaimsNoNodeIsToldHowToPickOne(t *testing.T) {
	t.Setenv("KNO_HOST", "tortuna")
	hosts := map[string]Host{"tortuna": {Hostname: "tortuna"}}
	c := Config{Network: "namenlos", Hosts: hosts}
	if !ClaimsNoNode(c) {
		t.Fatal("a host with no nodes does not count as claiming none")
	}
	var b bytes.Buffer
	Summary(&b, "/etc/katzenpost/kno.toml", c, "")
	if !strings.Contains(b.String(), "dpkg-reconfigure kno-host-config") {
		t.Errorf("the summary does not say how to pick a node:\n%s", b.String())
	}
	c.Nodes = []string{"gnunet"}
	if ClaimsNoNode(c) {
		t.Error("a host with a node counts as claiming none")
	}
}
