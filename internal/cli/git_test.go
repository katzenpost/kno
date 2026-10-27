package cli

import (
	"strings"
	"testing"
)

func TestGitFetchesGitHubOverHTTPS(t *testing.T) {
	got := strings.Join(GitArgs("clone", "git@github.com:katzenpost/kno"), " ")
	for _, want := range []string{
		"url.https://github.com/.insteadOf=git@github.com:",
		"url.https://github.com/.insteadOf=ssh://git@github.com/",
		"clone git@github.com:katzenpost/kno",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("git %s does not carry %s", got, want)
		}
	}
}
