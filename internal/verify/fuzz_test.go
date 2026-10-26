package verify

import "testing"

func FuzzParticipating(f *testing.F) {
	f.Add(participationDump, "wauland")
	f.Fuzz(func(t *testing.T, s, id string) {
		Participating(s, []NodeIdentifier{{"node", id}, {"authority", id}, {"any", id}})
	})
}
