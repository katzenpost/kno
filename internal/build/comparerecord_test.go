package build

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAPartialBuildThatMatchesTheRecordPasses(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(t.TempDir(), "pkgs")
	bin := "katzenpost_0.0.107+debian13_amd64.deb"
	cfg := "namenlos-config-mixy-pq-mixserver_0.0.107-1_amd64.deb"
	rel := CommittedDir(repo, "namenlos", "v0.0.107")
	node := NodeRecordDir(rel, "mixy")
	if err := os.MkdirAll(node, 0o755); err != nil {
		t.Fatal(err)
	}
	top := writePkg(t, dir, bin, "binary") + "  " + bin + "\n" +
		strings.Repeat("1", 64) + "  katzenpost_0.0.107+debian13_arm64.deb\n"
	if err := os.WriteFile(filepath.Join(rel, SumsName), []byte(top), 0o644); err != nil {
		t.Fatal(err)
	}
	mine := writePkg(t, dir, cfg, "config") + "  " + cfg + "\n"
	if err := os.WriteFile(filepath.Join(node, SumsName), []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	writePkg(t, dir, "kno_0.0.107-1_amd64.deb", "this kno")
	var out bytes.Buffer
	if rc := compareRecord(&out, repo, "namenlos", "v0.0.107", dir); rc != 0 {
		t.Fatalf("rc = %d, want 0:\n%s", rc, out.String())
	}
	if !strings.Contains(out.String(), "2 packages") {
		t.Errorf("the report does not count the 2 packages:\n%s", out.String())
	}
	writePkg(t, dir, cfg, "changed")
	out.Reset()
	if rc := compareRecord(&out, repo, "namenlos", "v0.0.107", dir); rc == 0 {
		t.Errorf("a changed package passed:\n%s", out.String())
	}
}
