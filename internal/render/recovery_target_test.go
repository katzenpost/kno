package render

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRenderRecoveryKeepsBackupWhenOutputIsSymlink(t *testing.T) {
	net := fixture(t)
	root := t.TempDir()
	out := filepath.Join(root, "generated")
	backup := backupPath(out)
	external := filepath.Join(root, "elsewhere")
	for _, dir := range []string{external, backup} {
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	keep := filepath.Join(backup, "keep")
	if err := os.WriteFile(keep, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, out); err != nil {
		t.Fatal(err)
	}
	if err := Render(net, out); err == nil {
		t.Fatal("unsafe generated output accepted")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("render recovery deleted backup before rejecting output symlink")
	}
}
