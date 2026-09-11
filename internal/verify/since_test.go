package verify

import (
	"bytes"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/track"
)

func TestTheJournalWindowIsAnchoredWhenGiven(t *testing.T) {
	r := &fakeRunner{
		out: map[string]string{"journalctl": "fetched a consensus"},
		rc:  map[string]int{},
	}
	o := simOptions(r, &fakeSystemd{available: false})
	o.Generic = false
	o.Dataplane = false
	o.ConsensusOnly = true
	o.Journalctl = "journalctl"
	o.Since = "@1700000000"

	o.Fetch = ""

	var out bytes.Buffer
	verdict(track.Logger{Tool: "verify", W: &out}, o)

	var saw string
	for _, c := range r.calls {
		if c.name == "journalctl" {
			saw = c.name + " " + strings.Join(c.args, " ")
		}
	}
	if saw == "" {
		t.Fatalf("the journal was not consulted:\n%s", out.String())
	}
	if !strings.Contains(saw, "@1700000000") {
		t.Errorf("the journal window was not the one given: %q", saw)
	}
}

func TestSinceFlagOverridesTheDefault(t *testing.T) {
	o, err := OptionsFor(nil, Flags{"VERIFY_SINCE": "@1700000000"})
	if err != nil {
		t.Fatal(err)
	}
	if o.Since != "@1700000000" {
		t.Errorf("Since = %q, want the flag's value", o.Since)
	}
	o2, err := OptionsFor(nil, Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if o2.Since != defaultSince {
		t.Errorf("Since = %q, want the default %q", o2.Since, defaultSince)
	}
}
