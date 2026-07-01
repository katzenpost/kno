package build

import (
	"testing"

	"github.com/katzenpost/kno/internal/config"
)

func TestTheLocalPeersApplyOnlyToABuildOfThisHost(t *testing.T) {
	t.Setenv("KNO_HOST", "mixy")
	c := fiveHosts()
	c.Peers = map[string]string{"annares": "10.0.0.2:30000"}
	for _, tc := range []struct {
		hosts []string
		local bool
	}{
		{[]string{"mixy"}, true},
		{[]string{"reed"}, false},
		{[]string{"mixy", "reed"}, false},
		{[]string{"annares", "haywood", "lilly", "mixy", "reed"}, false},
		{nil, false},
	} {
		got := localPeers(c, tc.hosts)
		if tc.local && got["annares"] != "10.0.0.2:30000" {
			t.Errorf("%v: peers %v, want this host's [peers]", tc.hosts, got)
		}
		if !tc.local && got != nil {
			t.Errorf("%v: peers %v. [peers] in kno.toml is a fallback for this host only,"+
				" not for the hosts that the organizer builds for", tc.hosts, got)
		}
	}
	if got := localPeers(config.Config{Peers: c.Peers}, []string{"mixy"}); got != nil {
		t.Errorf("a config without the host gave peers %v", got)
	}
}
