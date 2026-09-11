package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/record"
	"github.com/katzenpost/kno/internal/track"
)

func TestARollbackNeedsThePackagesTheHostRan(t *testing.T) {
	rel := t.TempDir()
	dir := filepath.Join(rel, "v0.0.104", "pkgs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"katzenpost_0.0.104+ubuntu26.04_ppc64el.deb", "kno_0.0.104-1_ppc64el.deb",
		"namenlos-config-annares-pq-authority_0.0.104-1_ppc64el.deb"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r := &RemoteRunner{Env: track.Env{ReleasesDir: rel}, Host: remoteHost{Label: "annares"}}
	if err := r.CanReinstall("v0.0.104"); err == nil {
		t.Error("a rollback without a sample of the host was allowed")
	}
	r.Before = &record.Sample{Packages: map[string]string{"katzenpost": "0.0.104+ubuntu26.04",
		"kno": "0.0.104-262", "namenlos-config-annares-pq-authority": "0.0.104-259"}}
	err := r.CanReinstall("v0.0.104")
	if err == nil || !strings.Contains(err.Error(), "kno 0.0.104-262") {
		t.Errorf("a rollback to other packages than the host ran gave %v", err)
	}
	r.Before.Packages = map[string]string{"katzenpost": "0.0.104", "kno": "0.0.104-1",
		"namenlos-config-annares-pq-authority": "0.0.104-1"}
	if err := r.CanReinstall("v0.0.104"); err != nil {
		t.Errorf("a rollback to the packages the host ran was refused: %v", err)
	}
}

func TestAHostAtItsLastGoodReleaseIsPromotedAtTheFirstPassingVerify(t *testing.T) {
	e := track.Env{StateDir: t.TempDir()}
	if sameRelease(e, "v0.0.107") {
		t.Error("a host with no last good release skips the wait")
	}
	if err := os.WriteFile(filepath.Join(e.StateDir, "last-known-good"), []byte("v0.0.104\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if sameRelease(e, "v0.0.107") {
		t.Error("a new katzenpost release skips the wait")
	}
	if err := os.WriteFile(filepath.Join(e.StateDir, "last-known-good"), []byte("v0.0.107\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !sameRelease(e, "v0.0.107") {
		t.Error("new config packages at the last good release wait 40 minutes")
	}
}
