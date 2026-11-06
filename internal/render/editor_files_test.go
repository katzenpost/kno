package render

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGenerateIgnoresEditorLockAndBackupFiles(t *testing.T) {
	net := fixture(t)
	nodes := filepath.Join(net, "editable-configs", "nodes")
	if err := os.Symlink("user@host.4242:1700000000", filepath.Join(nodes, ".#mixa-mix.toml")); err != nil {
		t.Fatal(err)
	}
	keys := filepath.Join(net, "editable-configs", "keys", "mix-id-keys")
	if err := os.Symlink("user@host.4242:1700000000", filepath.Join(keys, ".#mixa_id_pub_key.pem")); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(nodes, "mixa-mix.toml~"), "old")
	put(t, filepath.Join(nodes, "#mixa-mix.toml#"), "autosave")
	put(t, filepath.Join(nodes, ".mixa-mix.toml.swp"), "swap")
	if err := Generate(net, ""); err != nil {
		t.Fatalf("editor files beside the node files stopped the render: %v", err)
	}
}

func TestInterruptedRenderStageIsIgnoredByGit(t *testing.T) {
	stage := "data/networks/.kno-render-stage-1234/configs/mixa-pq-mixserver.toml"
	if err := exec.Command("git", "-C", "../..", "check-ignore", "-q", "--no-index", stage).Run(); err != nil {
		t.Fatalf("git does not ignore %s: %v", stage, err)
	}
}
