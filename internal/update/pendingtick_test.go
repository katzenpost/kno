package update

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/track"
)

func TestAPendingHostIsNotPreflightedAsANewDeployment(t *testing.T) {
	state := t.TempDir()
	e := track.Env{StateDir: state}
	pending := knopath.HostState(state, "reed")
	if err := os.MkdirAll(pending, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pending, "state"), []byte("pending\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hosts := []remoteHost{{Label: "reed"}, {Label: "mixy"}}
	got := deploying(e, hosts)
	if len(got) != 1 || got[0].Label != "mixy" {
		t.Fatalf("deploying = %+v, want only mixy: reed's tick verifies and promotes, it deploys nothing", got)
	}
}
