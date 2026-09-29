package deb

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

func TestMakefileDebVersionMatchesGo(t *testing.T) {
	gmake := gnuMake(t)
	repo := testutil.RepoRoot(t)
	out, err := exec.Command(gmake, "--no-print-directory", "-C",
		testutil.ModuleRoot(t), "print-debver").Output()
	if err != nil {
		t.Fatalf("make print-debver: %v", err)
	}
	ref := pinnedRef(t, repo)
	rev := RepoRevision(repo, ref)
	recs, _ := filepath.Glob(filepath.Join(repo, "networks", "*", ".generated", "releases", ref, "packages.json"))
	for _, r := range recs {
		var h struct {
			ToolRevision string `json:"tool_revision"`
		}
		if b, err := os.ReadFile(r); err == nil && json.Unmarshal(b, &h) == nil && h.ToolRevision != "" {
			rev = h.ToolRevision
		}
	}
	want := ToolVersionRev(repo, ref, rev)
	if got := strings.TrimSpace(string(out)); got != want {
		t.Fatalf("make says %q, ToolVersion says %q -- the two derivations"+
			" have drifted, which is the defect that shipped a stale kno",
			got, want)
	}
}
