package deb

import (
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/service"
)

func TestScriptsStopAUnitWhoseFileIsGone(t *testing.T) {
	s := service.Service{Unit: "kpserver@x-pq-mixserver", LegacyUnit: "pq-katzenpost-mixserver",
		StateDir: "/var/lib/katzenpost/server/x-pq-mixserver", LegacyStateDir: "/var/lib/pq-katzenpost-mixserver"}
	for name, script := range map[string]string{
		"prerm":   upstreamPrerm(s),
		"preinst": upstreamPreinst("namenlos-config-x-pq-mixserver", s),
	} {
		if strings.Contains(script, "disable --now") {
			t.Errorf("%s uses disable --now; when the unit file is already gone disable fails and nothing stops the process:\n%s", name, script)
		}
		if !strings.Contains(script, "systemctl stop ") {
			t.Errorf("%s never runs systemctl stop", name)
		}
	}
}
