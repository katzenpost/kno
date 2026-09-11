package verify

import (
	"bytes"
	"testing"

	"github.com/katzenpost/kno/internal/track"
)

func TestNoResolvedUnitsIsNotHealthy(t *testing.T) {
	var out bytes.Buffer
	o := simOptions(okRunner(), &fakeSystemd{available: true})
	o.Generic = false
	o.Dataplane = false
	o.Units = func() ([]string, error) { return nil, nil }

	if code := verdict(track.Logger{Tool: "verify", W: &out}, o); code == CodeHealthy {
		t.Errorf("a host that resolved no units reported healthy: %s", out.String())
	}
}
