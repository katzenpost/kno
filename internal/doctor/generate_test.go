package doctor

import "testing"

func TestHasSubid(t *testing.T) {
	const body = "user:100000:65536\nkno:165536:65536\n# a comment\nmalformed\n"
	for acct, want := range map[string]bool{
		"kno":    true,
		"user":   true,
		"nobody": false,
		"":       false,

		"kn": false,
	} {
		if got := hasSubid(body, acct); got != want {
			t.Errorf("hasSubid(%q) = %v, want %v", acct, got, want)
		}
	}
}
