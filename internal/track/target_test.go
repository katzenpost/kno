package track

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func targetEnv(t *testing.T, tags ...string) Env {
	t.Helper()
	needTools(t, "git")
	dir := t.TempDir()
	repo := filepath.Join(dir, "network")
	kp := filepath.Join(dir, "katzenpost")
	state := filepath.Join(dir, "state")

	initRepo(t, repo)
	initRepo(t, kp, tags...)
	writeVersions(t, repo, strings.Join(tags, "\n")+"\n")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	return Env{
		Repo: repo, KatzenpostRepo: kp, StateDir: state,
		QuarantineFile: filepath.Join(state, "quarantine"),
		CurrentFile:    filepath.Join(state, "current"),
	}
}

func put(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAQuarantinedReleaseIsNotADowngradeInstruction(t *testing.T) {
	e := targetEnv(t, "v0.0.102", "v0.0.103", "v0.0.104")
	put(t, e.CurrentFile, "v0.0.104\n")
	put(t, e.QuarantineFile, "v0.0.104\n")

	got, why := e.Target()
	if got != "v0.0.104" {
		t.Fatalf("Target = %q (%s), want the host to stay on v0.0.104", got, why)
	}

	for _, want := range []string{"v0.0.104", "quarantined", "kno unquarantine"} {
		if !strings.Contains(why, want) {
			t.Errorf("the hold does not mention %q:\n%s", want, why)
		}
	}
}

func TestAHostHeldBelowAQuarantinedReleaseIsToldWhy(t *testing.T) {
	e := targetEnv(t, "v0.0.102", "v0.0.104")
	put(t, e.CurrentFile, "v0.0.102\n")
	put(t, e.QuarantineFile, "v0.0.104\n")

	got, why := e.Target()
	if got != "v0.0.102" {
		t.Fatalf("Target = %q, want the host to stay on v0.0.102", got)
	}

	for _, want := range []string{"v0.0.104", "quarantined", "kno unquarantine"} {
		if !strings.Contains(why, want) {
			t.Errorf("the hold does not mention %q:\n%s", want, why)
		}
	}
}

func TestAHealthyHostIsNotToldItIsHeldBack(t *testing.T) {
	e := targetEnv(t, "v0.0.102", "v0.0.104")
	put(t, e.CurrentFile, "v0.0.104\n")
	if got, why := e.Target(); got != "v0.0.104" || why != "" {
		t.Errorf("Target = %q, note %q; want v0.0.104 with nothing to explain", got, why)
	}
}

func TestTheFloorFallsBackToLastKnownGood(t *testing.T) {
	e := targetEnv(t, "v0.0.100", "v0.0.102", "v0.0.104")
	put(t, filepath.Join(e.StateDir, "last-known-good"), "v0.0.102\n")
	put(t, e.QuarantineFile, "v0.0.104\n")

	if got, why := e.Target(); got != "v0.0.102" {
		t.Fatalf("Target = %q (%s), want v0.0.102 -- v0.0.100 is backwards", got, why)
	}
}

func TestWithNoRecordedReleaseAQuarantinedNewestTagRefuses(t *testing.T) {
	e := targetEnv(t, "v0.0.102", "v0.0.104")
	put(t, e.QuarantineFile, "v0.0.104\n")

	got, why := e.Target()
	if got != "" {
		t.Fatalf("Target = %q, want nothing rather than the older v0.0.102", got)
	}
	for _, want := range []string{"v0.0.104", "quarantined", "downgrade", "kno unquarantine"} {
		if !strings.Contains(why, want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, why)
		}
	}
}

func TestTargetStillPicksTheNewestUsableTag(t *testing.T) {
	e := targetEnv(t, "v0.0.102", "v0.0.104")
	put(t, e.CurrentFile, "v0.0.102\n")
	if got, why := e.Target(); got != "v0.0.104" {
		t.Errorf("Target = %q (%s), want v0.0.104", got, why)
	}

	put(t, e.CurrentFile, "v0.0.104\n")
	if got, _ := e.Target(); got != "v0.0.104" {
		t.Errorf("Target = %q, want the host to stay at v0.0.104", got)
	}

	put(t, e.CurrentFile, "")
	if got, _ := e.Target(); got != "v0.0.104" {
		t.Errorf("Target = %q on a fresh host, want v0.0.104", got)
	}
}

func TestANewerTagStillUpdatesPastAQuarantinedOne(t *testing.T) {
	e := targetEnv(t, "v0.0.102", "v0.0.104", "v0.0.105")
	put(t, e.CurrentFile, "v0.0.104\n")
	put(t, e.QuarantineFile, "v0.0.104\n")
	if got, why := e.Target(); got != "v0.0.105" {
		t.Fatalf("Target = %q (%s), want v0.0.105", got, why)
	}
}

func TestTrackSaysWhyTheHostIsHeld(t *testing.T) {
	e := targetEnv(t, "v0.0.102", "v0.0.104")
	put(t, e.CurrentFile, "v0.0.102\n")
	put(t, e.QuarantineFile, "v0.0.104\n")
	var stderr strings.Builder
	if code := e.report(io.Discard, Logger{Tool: "track", W: &stderr}); code != 3 {
		t.Fatalf("exit = %d, want 3:\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "kno unquarantine v0.0.104") {
		t.Errorf("track does not say why the host stays on v0.0.102:\n%s", stderr.String())
	}
}

func TestADryRunTrackDoesNotCloneOrFetch(t *testing.T) {
	needTools(t, "git")
	home := t.TempDir()
	put(t, filepath.Join(home, "kno.toml"), "network = \"testnet\"\n")
	for k, v := range map[string]string{
		"HOME": home, "KNO_HOME": home, "KNO_REPO": filepath.Join(home, "absent"),
		"KNO_CONFIG": filepath.Join(home, "kno.toml"), "KNO_URL": t.TempDir(),
		"KATZENPOST_REPO": filepath.Join(home, "katzenpost"), "KNO_TRACK_FETCH": "1",
		"KNO_CLONE": "1", "KNO_CLONE_KP": "1", "KNO_GIT_FETCH": "1",
	} {
		t.Setenv(k, v)
	}
	var stderr strings.Builder
	Run(io.Discard, &stderr, []string{"--dry-run"})
	if !strings.Contains(stderr.String(), "network repo missing") {
		t.Fatalf("the test does not reach the repo check:\n%s", stderr.String())
	}
	if strings.Contains(stderr.String(), "cloning") {
		t.Errorf("kno track --dry-run cloned:\n%s", stderr.String())
	}
	if _, err := os.Stat(CloneDir(home)); err == nil {
		t.Errorf("kno track --dry-run made %s", CloneDir(home))
	}
}

func TestTrackReadsTheQuarantineAsStatusDoes(t *testing.T) {
	dir := t.TempDir()
	e := Env{QuarantineFile: filepath.Join(dir, "quarantine")}
	put(t, e.QuarantineFile, "v0.0.103\r\n v0.0.104 \n")
	for _, tag := range []string{"v0.0.103", "v0.0.104"} {
		if !e.Quarantined(tag) {
			t.Errorf("%s is in the file, and kno status calls it quarantined", tag)
		}
	}
	if e.Quarantined("v0.0.10") {
		t.Error("a prefix of a quarantined tag counts as quarantined")
	}
}
