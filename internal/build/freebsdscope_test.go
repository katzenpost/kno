package build

import (
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

func TestAFreeBSDHostNeedsNoDeb(t *testing.T) {
	repo := testutil.RepoRoot(t)
	f := BackendFormats(repo, "namenlos", []string{"respectmy-pq-authority", "allmixedup-pq-gatewaynode"})
	if f["deb"] || !f["freebsd"] {
		t.Fatalf("formats for the jail host = %v, want freebsd only", f)
	}
	o := Options{Repo: repo, Network: "namenlos", Arches: []string{"amd64"},
		Services: []string{"respectmy-pq-authority"}, SkipDeb: true}
	if p := o.platforms(); len(p) != 0 {
		t.Errorf("a build with no deb compiles katzenpost for %v", p)
	}
	if got := o.formats(); got != "freebsd+images" {
		t.Errorf("formats() = %q", got)
	}
}
