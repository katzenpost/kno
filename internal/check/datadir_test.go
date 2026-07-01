package check

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/service"
)

func TestASystemdServiceRunsFromItsUnitsStateDir(t *testing.T) {
	repo := t.TempDir()
	cfgs := knopath.ConfigsDir(repo, "n")
	if err := os.MkdirAll(cfgs, 0o755); err != nil {
		t.Fatal(err)
	}
	sv := service.Service{Name: "m-pq-mixserver", Backend: "systemd", StateDir: "/var/lib/katzenpost/server/m-pq-mixserver"}
	write := func(dir string) {
		body := "[Server]\n  Identifier = \"m\"\n  DataDir = \"" + dir + "\"\n"
		if err := os.WriteFile(filepath.Join(cfgs, sv.Name+".toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(sv.StateDir)
	if err := dataDirMatches(repo, "n", sv); err != nil {
		t.Errorf("a matching DataDir was refused: %v", err)
	}
	write("/var/lib/pq-katzenpost-mixserver")
	if err := dataDirMatches(repo, "n", sv); err == nil {
		t.Error("a DataDir the unit does not create was accepted")
	}
	sv.Backend = "docker"
	if err := dataDirMatches(repo, "n", sv); err != nil {
		t.Errorf("a docker service was held to the systemd layout: %v", err)
	}
}
