package doctor

import (
	"os/user"
	"strings"
	"testing"
)

func TestAMissingAccountIsAFailureAndNamesWhatWantedIt(t *testing.T) {
	f := hostFacts{
		UpdaterUser: "kno-nonexistent-test-account",
		Units: []unitReport{
			{Unit: "pq-katzenpost-authority", User: "kno-nonexistent-test-account"},
			{Unit: "pq-katzenpost-mixserver", User: "kno-nonexistent-test-account"},
		},
	}
	got := gatherAccounts(f)
	if len(got) != 1 {
		t.Fatalf("one account named three times should be one entry, got %d: %+v", len(got), got)
	}
	a := got[0]
	if a.Exists() {
		t.Skip("an account by that name really exists here")
	}

	for _, want := range []string{"kno updater", "pq-katzenpost-authority", "pq-katzenpost-mixserver"} {
		if !strings.Contains(a.Why, want) {
			t.Errorf("Why = %q, want it to name %q", a.Why, want)
		}
	}
	if a.UID != -1 || a.GID != -1 {
		t.Errorf("a missing account reported ids: uid=%d gid=%d", a.UID, a.GID)
	}
}

func TestAnExistingAccountCarriesItsIDs(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Skip("no current user")
	}
	got := gatherAccounts(hostFacts{UpdaterUser: me.Username})
	if len(got) != 1 {
		t.Fatalf("want one account, got %+v", got)
	}
	if !got[0].Exists() {
		t.Fatalf("%s exists and was reported missing", me.Username)
	}
	if got[0].UID < 0 || got[0].GID < 0 {
		t.Errorf("uid/gid not resolved: %+v", got[0])
	}
}

func TestAUnitWithNoAccountIsNotReported(t *testing.T) {
	got := gatherAccounts(hostFacts{Units: []unitReport{{Unit: "kpclientd", User: ""}}})
	if len(got) != 0 {
		t.Errorf("a unit naming no account produced %+v", got)
	}
}
