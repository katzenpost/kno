package update

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/build"
	"github.com/katzenpost/kno/internal/track"
)

func TestTheRecordGatesWhatTheHostInstalls(t *testing.T) {
	repo := t.TempDir()
	pkgs := filepath.Join(t.TempDir(), "pkgs")
	if err := os.MkdirAll(pkgs, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "namenlos-config-allmixedup-pq-gatewaynode-0.0.107.20261020.gabc.pkg"
	if err := os.WriteFile(filepath.Join(pkgs, name), []byte("built"), 0o644); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	r := ToolRunner{Env: track.Env{Repo: repo, Network: "namenlos"}, Log: track.Logger{Tool: "update", W: &log}}

	if err := r.checkRecord("v0.0.107", pkgs); err != nil || !strings.Contains(log.String(), "only with itself") {
		t.Fatalf("no record: err=%v log=%q", err, log.String())
	}

	rec := build.CommittedDir(repo, "namenlos", "v0.0.107")
	if err := os.MkdirAll(rec, 0o755); err != nil {
		t.Fatal(err)
	}
	sums := filepath.Join(rec, build.SumsName)
	if err := os.WriteFile(sums, []byte(strings.Repeat("0", 64)+"  katzenpost-0.0.107.pkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	log.Reset()
	if err := r.checkRecord("v0.0.107", pkgs); err != nil || !strings.Contains(log.String(), "names none of the packages of this host") {
		t.Fatalf("a record that names none of this host's packages: err=%v log=%q", err, log.String())
	}

	kp := filepath.Join(pkgs, "katzenpost-0.0.107.pkg")
	if err := os.WriteFile(kp, []byte("kp"), 0o644); err != nil {
		t.Fatal(err)
	}
	kpSum := sha256.Sum256([]byte("kp"))
	if err := os.WriteFile(sums, []byte(hex.EncodeToString(kpSum[:])+"  katzenpost-0.0.107.pkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	log.Reset()
	err := r.checkRecord("v0.0.107", pkgs)
	if err == nil || !strings.Contains(err.Error(), "records no digest for: "+name) {
		t.Errorf("a package the record does not name: err=%v", err)
	}
	if strings.Contains(log.String(), "matches the record") {
		t.Errorf("a refused build logged a match: %q", log.String())
	}

	sum := sha256.Sum256([]byte("built"))
	if err := os.WriteFile(sums, []byte(hex.EncodeToString(kpSum[:])+"  katzenpost-0.0.107.pkg\n"+hex.EncodeToString(sum[:])+"  "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	log.Reset()
	if err := r.checkRecord("v0.0.107", pkgs); err != nil || !strings.Contains(log.String(), "v0.0.107 matches the record") {
		t.Errorf("a build the record names: err=%v log=%q", err, log.String())
	}
}
