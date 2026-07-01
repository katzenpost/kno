package build

import (
	"github.com/katzenpost/kno/internal/knopath"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

func TestGcServicesPacksEveryNodeRoleAndNothingElse(t *testing.T) {
	repo := testutil.RepoRoot(t)
	gc := knopath.ConfigsDir(repo, "namenlos")

	ents, err := os.ReadDir(gc)
	if err != nil {
		t.Fatal(err)
	}
	roles := []string{"-pq-authority", "-pq-mixserver", "-pq-gatewaynode",
		"-pq-servicenode", "-pq-storagenode"}
	var want, skipped []string
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".toml")
		isNode := false
		for _, r := range roles {

			if strings.HasSuffix(name, r) || strings.Contains(name, r+"-") {
				isNode = true
				break
			}
		}
		if isNode {
			want = append(want, name)
		} else {
			skipped = append(skipped, name)
		}
	}
	sort.Strings(want)
	if len(want) == 0 {
		t.Fatal("no node configs in .generated/configs; this test covers nothing")
	}
	if len(skipped) == 0 {
		t.Fatal("nothing was excluded; the filter is not being exercised")
	}

	got := gcServices(repo, "namenlos")
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("service set differs:\n  got  %v\n  want %v", got, want)
	}
	for _, s := range skipped {
		for _, g := range got {
			if g == s {
				t.Errorf("%s is not a node config but would be packaged", s)
			}
		}
	}
	t.Logf("%d services packaged, %d non-node configs excluded", len(got), len(skipped))
}
