package build

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTwoNodeRecordsThatDisagreeAreAMismatch(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(t.TempDir(), "pkgs")
	name := "namenlos-config-mixy-pq-mixserver_0.0.107-1_amd64.deb"
	sum := writePkg(t, dir, name, "what the second record says")
	rel := CommittedDir(repo, "namenlos", "v0.0.107")
	other := writePkg(t, t.TempDir(), "x", "what the first record says")
	for node, s := range map[string]string{"a": other, "b": sum} {
		d := NodeRecordDir(rel, node)
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, SumsName), []byte(s+"  "+name+"\n"), 0o644)
	}
	os.WriteFile(filepath.Join(rel, SumsName), []byte(""), 0o644)
	d, err := DiffAgainstCommitted(repo, "namenlos", "v0.0.107", dir, "")
	if err == nil && len(d.Mismatched) == 0 {
		t.Errorf("nodes a and b record different digests for %s, and the build passed", name)
	}
}
