package deb

import (
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/service"
)

func TestConfValuesReachingRootAreChecked(t *testing.T) {
	for _, tc := range []struct{ what, value string }{
		{"STATE_DIR", "/var/lib/x /"},
		{"STATE_DIR", "/var/lib/../../etc"},
		{"STATE_DIR", "/var/lib/x; chmod u+s /bin/sh"},
		{"SERVICE_USER", "svc; id"},
		{"SERVICE_USER", "-svc"},
		{"CONFIG_DST", "/etc/x/../../../root/.ssh/authorized_keys"},
		{"UNIT", "kp; reboot"},
	} {
		s := goodService()
		switch tc.what {
		case "STATE_DIR":
			s.StateDir = tc.value
		case "SERVICE_USER":
			s.ServiceUser = tc.value
		case "CONFIG_DST":
			s.ConfigDst = tc.value
		case "UNIT":
			s.Unit = tc.value
		}
		err := CheckServiceValues(s)
		if err == nil {
			t.Errorf("%s=%q accepted; it reaches a root script", tc.what, tc.value)
			continue
		}
		if !strings.Contains(err.Error(), tc.what) {
			t.Errorf("%s=%q refused without naming the field: %v", tc.what, tc.value, err)
		}
	}
}

func TestRealConfValuesPass(t *testing.T) {
	if err := CheckServiceValues(goodService()); err != nil {
		t.Errorf("a real service was refused: %v", err)
	}
}

func goodService() service.Service {
	return service.Service{
		Name:        "annares-pq-authority",
		ServiceUser: "pq-katzenpost-authority",
		StateDir:    "/var/lib/pq-katzenpost-authority",
		ConfigDst:   "/etc/pq-katzenpost-authority/authority.toml",
		Unit:        "pq-katzenpost-authority",
	}
}
