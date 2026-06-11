package track

import "testing"

func TestAFloorMissingFromTheTagListIsNotADowngrade(t *testing.T) {
	e := targetEnv(t, "v0.0.101", "v0.0.102")
	put(t, e.CurrentFile, "v0.0.103\n")

	got, why := e.Target()
	if got == "v0.0.101" || got == "v0.0.102" {
		t.Fatalf("Target = %q, a downgrade from v0.0.103 reported as an update (%s)", got, why)
	}
}

func TestANewerTagIsStillAnUpgrade(t *testing.T) {
	e := targetEnv(t, "v0.0.103", "v0.0.104")
	put(t, e.CurrentFile, "v0.0.103\n")

	if got, why := e.Target(); got != "v0.0.104" {
		t.Fatalf("Target = %q, want v0.0.104 (%s)", got, why)
	}
}
