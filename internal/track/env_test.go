package track

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/katzenpost/kno/internal/knopath"
)

func loadEnvIn(t *testing.T, home string, env map[string]string) Env {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "kno.toml")
	if err := os.WriteFile(cfg, []byte("network = \"namenlos\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"HOME": home, "KNO_CONFIG": cfg, "KNO_HOME": "",
		"RELEASES_DIR": "", "UPDATER_STATE_DIR": "",
		"QUARANTINE_FILE": "", "CURRENT_FILE": "",
	} {
		t.Setenv(k, v)
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	e, err := LoadEnv()
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestLoadEnvKeepsAPersonsDirectoriesUnderTheArtifactsRoot(t *testing.T) {
	art := t.TempDir()
	e := loadEnvIn(t, t.TempDir(), map[string]string{"KNO_ARTIFACTS": art})
	if got, want := e.ReleasesDir, filepath.Join(art, "releases"); got != want {
		t.Errorf("ReleasesDir = %q, want %q", got, want)
	}
	if got, want := e.StateDir, filepath.Join(art, "state"); got != want {
		t.Errorf("StateDir = %q, want %q", got, want)
	}
	if got, want := e.QuarantineFile, filepath.Join(art, "state", "quarantine"); got != want {
		t.Errorf("QuarantineFile = %q, want %q", got, want)
	}

	if got, want := filepath.Dir(e.ReleasesDir), knopath.Dist(); got != want {
		t.Errorf("a tick would build into %q, not %q", got, want)
	}
}

func TestLoadEnvLeavesTheUpdaterAccountWhereItIs(t *testing.T) {
	art := t.TempDir()
	e := loadEnvIn(t, knopath.UpdaterHome, map[string]string{"KNO_ARTIFACTS": art})
	if got, want := e.ReleasesDir, "/var/lib/kno-updater/releases"; got != want {
		t.Errorf("ReleasesDir = %q, want %q", got, want)
	}
	if got, want := e.StateDir, "/var/lib/kno-updater/state"; got != want {
		t.Errorf("StateDir = %q, want %q", got, want)
	}

	e = loadEnvIn(t, "/nonsense", map[string]string{
		"KNO_HOME": "/srv/kno-account", "KNO_ARTIFACTS": art})
	if got, want := e.ReleasesDir, "/srv/kno-account/releases"; got != want {
		t.Errorf("KNO_HOME ReleasesDir = %q, want %q", got, want)
	}
	if got, want := e.StateDir, "/srv/kno-account/state"; got != want {
		t.Errorf("KNO_HOME StateDir = %q, want %q", got, want)
	}
}

func TestLoadEnvHonoursTheDirectoryOverrides(t *testing.T) {
	e := loadEnvIn(t, t.TempDir(), map[string]string{
		"RELEASES_DIR": "/srv/kno/releases", "UPDATER_STATE_DIR": "/srv/kno/state"})
	if e.ReleasesDir != "/srv/kno/releases" {
		t.Errorf("ReleasesDir = %q, want the override", e.ReleasesDir)
	}
	if e.StateDir != "/srv/kno/state" {
		t.Errorf("StateDir = %q, want the override", e.StateDir)
	}
	if got, want := e.CurrentFile, "/srv/kno/state/current"; got != want {
		t.Errorf("CurrentFile = %q, want %q", got, want)
	}
}
