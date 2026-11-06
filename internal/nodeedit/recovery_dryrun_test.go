package nodeedit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/katzenpost/kno/internal/cli"
)

func TestEditorDryRunDoesNotRecoverAnInterruptedPublication(t *testing.T) {
	repo := lifecycleRepo(t)
	network := filepath.Join(repo, "networks", "namenlos")
	backup := network + ".kno-nodeedit-backup"
	if err := os.Rename(network, backup); err != nil {
		t.Fatal(err)
	}
	state := network + ".kno-nodeedit-state"
	if err := os.WriteFile(state, []byte("prepared\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KNO_REPO", repo)
	cli.SetDryRun(true)
	t.Cleanup(func() { cli.SetDryRun(false) })
	_, _ = runEditorCommand(t, "", "topology", "move", "windfall", "--layer", "0", "--network", "namenlos", "--write")
	if _, err := os.Stat(network); !os.IsNotExist(err) {
		t.Fatal("dry run restored or changed the unpublished network")
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatal("dry run removed the recovery backup")
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatal("dry run removed the recovery state")
	}
	cli.SetDryRun(false)
	if _, err := runEditorCommand(t, "", "recover", "--network", "namenlos"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(network); !os.IsNotExist(err) {
		t.Fatal("recovery preview changed the network")
	}
	if _, err := runEditorCommand(t, "", "recover", "--network", "namenlos", "--write"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(network); err != nil {
		t.Fatal("explicit recovery did not restore the previous network")
	}
}
