package topology

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

func TestLintTheRealNetworks(t *testing.T) {
	repo := testutil.RepoRoot(t)
	known, err := KnownFindings(repo)
	if err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(filepath.Join(repo, "networks"))
	if err != nil {
		t.Fatal(err)
	}
	var nets []string
	for _, e := range ents {
		if e.IsDir() {
			nets = append(nets, e.Name())
		}
	}
	for _, net := range nets {
		f, err := Lint(repo, net)
		if err != nil {
			t.Errorf("%s: %v", net, err)
			continue
		}
		t.Logf("%s: %d finding(s)", net, len(f))
		for _, x := range f {
			if Known(known, x) {
				t.Logf("  known  %s", x)
				continue
			}
			t.Errorf("  NEW    %s: %s", net, x)
		}
	}
}
