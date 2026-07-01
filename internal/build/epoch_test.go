package build

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEpochCommandPrintsTheRecordedEpoch(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "networks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "thirdparty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "thirdparty", "pins.env"),
		[]byte("KATZENPOST_REF=v0.0.107\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := CommittedDir(repo, "namenlos", "v0.0.107")
	if err := os.MkdirAll(rec, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rec, ManifestName),
		[]byte(`{"tag":"v0.0.107","source_date_epoch":"1780506605"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KNO_REPO", repo)
	t.Setenv("SOURCE_DATE_EPOCH", "")

	run := func() string {
		var out bytes.Buffer
		cmd := epochCommand()
		cmd.SetOut(&out)
		cmd.SetArgs(nil)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out.String())
	}
	if got := run(); got != "1780506605" {
		t.Errorf("epoch = %q, want the recorded 1780506605", got)
	}
	t.Setenv("SOURCE_DATE_EPOCH", "42")
	if got := run(); got != "42" {
		t.Errorf("epoch = %q, want SOURCE_DATE_EPOCH from the environment", got)
	}
}
