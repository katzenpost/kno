package layout

import (
	"testing"

	"github.com/katzenpost/kno/internal/service"
)

func TestTheLayoutFollowsTheRelease(t *testing.T) {
	s := service.Service{
		Unit: "kpserver@annares-pq-servicenode", LegacyUnit: "pq-katzenpost-mixserver",
		StateDir:       "/var/lib/katzenpost/server/annares-pq-servicenode",
		LegacyStateDir: "/var/lib/pq-katzenpost-mixserver",
	}
	for _, c := range []struct {
		version, unit, dir string
	}{
		{"v0.0.104", s.LegacyUnit, s.LegacyStateDir},
		{"0.0.104", s.LegacyUnit, s.LegacyStateDir},
		{"v0.0.107", s.Unit, s.StateDir},
		{"v0.0.108", s.Unit, s.StateDir},
		{"", s.Unit, s.StateDir},
	} {
		if got := Unit(s, c.version); got != c.unit {
			t.Errorf("Unit(%q) = %q, want %q", c.version, got, c.unit)
		}
		if got := StateDir(s, c.version); got != c.dir {
			t.Errorf("StateDir(%q) = %q, want %q", c.version, got, c.dir)
		}
	}
	bare := service.Service{Unit: "kpserver@x", StateDir: "/var/lib/katzenpost/server/x"}
	if got := Unit(bare, "v0.0.104"); got != bare.Unit {
		t.Errorf("a service with no legacy unit got %q", got)
	}
	if got := StateDir(bare, "v0.0.104"); got != bare.StateDir {
		t.Errorf("a service with no legacy state dir got %q", got)
	}
}
