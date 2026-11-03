package nodeedit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLifecycleRecoveryKeepsBackupWhenTargetIsSymlink(t *testing.T) {
	repo := t.TempDir()
	net := filepath.Join(repo, "networks", "n")
	backup, state := net+".kno-nodeedit-backup", net+".kno-nodeedit-state"
	external := filepath.Join(repo, "elsewhere")
	for _, dir := range []string{external, backup} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	keep := filepath.Join(backup, "keep")
	if err := os.WriteFile(keep, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, net); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, []byte("published\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KNO_REPO", repo)
	_, _ = runEditorCommand(t, "", "recover", "--network", "n", "--write")
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("lifecycle recovery deleted backup while target was a symlink")
	}
}
