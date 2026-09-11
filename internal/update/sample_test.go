package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/record"
	"github.com/katzenpost/kno/internal/track"
)

func TestASampleReadsPackagesAndUnits(t *testing.T) {
	out := "katzenpost 0.0.104\nkno 0.0.104-83\nnamenlos-config-mixy-pq-mixserver 0.0.104\n" +
		"#units\nactive\n"
	pkgs, units := parseSample(out, []string{"pq-katzenpost-mixserver"})
	if pkgs["katzenpost"] != "0.0.104" || pkgs["kno"] != "0.0.104-83" {
		t.Errorf("packages: %v", pkgs)
	}
	if units["pq-katzenpost-mixserver"] != "active" {
		t.Errorf("units: %v", units)
	}

	pkgs, units = parseSample("#units\nPermission denied\n", []string{"a", "b"})
	if len(pkgs) != 0 {
		t.Errorf("invented packages: %v", pkgs)
	}
	if len(units) != 0 {
		t.Errorf("ssh noise became a unit state: %v", units)
	}
}

func hostState(t *testing.T, dir string, files map[string]string) track.Env {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for n, body := range files {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return track.Env{
		StateDir:       dir,
		CurrentFile:    filepath.Join(dir, "current"),
		QuarantineFile: filepath.Join(dir, "quarantine"),
	}
}

func mixyHost() remoteHost {
	return remoteHost{Label: "mixy", Node: "root@mixy", Services: []string{"mixy-pq-mixserver"}}
}

func TestARecordSaysWhenADeploymentIsStillInFlight(t *testing.T) {
	repo := t.TempDir()
	e := track.Env{Repo: repo, Network: "namenlos"}
	he := hostState(t, filepath.Join(t.TempDir(), "mixy"), map[string]string{
		"state":   "pending\n",
		"current": "v0.0.104\n",
		"pending": "tag=v0.0.104\nprev=v0.0.102\ndeadline=1789419519\n",
	})
	before := &record.Sample{Packages: map[string]string{"katzenpost": "0.0.102"}}

	r := recordFor(e, he, mixyHost(), "v0.0.104", before, nil, "laptop")
	if r.Deploying == nil {
		t.Fatal("a pending release is not recorded as being deployed")
	}
	if r.Deploying.Tag != "v0.0.104" || r.Deploying.From != "v0.0.102" {
		t.Errorf("deploying: %+v", r.Deploying)
	}
	if r.Deployed != nil {
		t.Error("an unconfirmed release was recorded as what the host is running")
	}
	if r.Observed == nil || r.Observed.Packages["katzenpost"] != "0.0.102" {
		t.Errorf("what it was running was not kept: %+v", r.Observed)
	}
	if got := r.Summary(); got != "installed v0.0.104 (was v0.0.102), in the verify window" {
		t.Errorf("Summary = %q", got)
	}
}

func TestAConfirmedDeploymentRecordsTheAfterReading(t *testing.T) {
	repo := t.TempDir()
	e := track.Env{Repo: repo, Network: "namenlos"}
	dir := filepath.Join(t.TempDir(), "mixy")

	he := hostState(t, dir, map[string]string{
		"state":   "pending\n",
		"current": "v0.0.104\n",
		"pending": "tag=v0.0.104\nprev=v0.0.102\n",
	})
	first := recordFor(e, he, mixyHost(), "v0.0.104",
		&record.Sample{Packages: map[string]string{"katzenpost": "0.0.102"}}, nil, "laptop")
	if _, err := record.Write(repo, "namenlos", first); err != nil {
		t.Fatal(err)
	}

	he = hostState(t, dir, map[string]string{"state": "steady\n", "current": "v0.0.104\n"})
	if err := os.Remove(filepath.Join(dir, "pending")); err != nil {
		t.Fatal(err)
	}
	after := &record.Sample{
		Packages:  map[string]string{"katzenpost": "0.0.104"},
		Units:     map[string]string{"pq-katzenpost-mixserver": "active"},
		Consensus: "in",
	}
	r := recordFor(e, he, mixyHost(), "v0.0.104", nil, after, "laptop")

	if r.Deploying != nil {
		t.Error("a confirmed release is still recorded as in flight")
	}
	if r.Deployed == nil || r.Deployed.Packages["katzenpost"] != "0.0.104" {
		t.Fatalf("what it is running now: %+v", r.Deployed)
	}
	if r.Succeeded == nil || r.Succeeded.From != "v0.0.102" {
		t.Errorf("the outcome does not say where it came from: %+v", r.Succeeded)
	}

	if r.Observed == nil || r.Observed.Packages["katzenpost"] != "0.0.102" {
		t.Errorf("what it was running was lost: %+v", r.Observed)
	}
}

func TestARollbackIsRecordedAsOne(t *testing.T) {
	repo, state := t.TempDir(), t.TempDir()
	dir := filepath.Join(state, "mixy")
	he := hostState(t, dir, map[string]string{
		"state":      "pending\n",
		"current":    "v0.0.104\n",
		"pending":    "tag=v0.0.104\nprev=v0.0.102\n",
		"quarantine": "",
	})
	e := track.Env{Repo: repo, Network: "namenlos", QuarantineFile: filepath.Join(dir, "quarantine")}
	first := recordFor(e, he, mixyHost(), "v0.0.104",
		&record.Sample{Packages: map[string]string{"katzenpost": "0.0.102"}}, nil, "laptop")
	if _, err := record.Write(repo, "namenlos", first); err != nil {
		t.Fatal(err)
	}

	he = hostState(t, dir, map[string]string{
		"state":      "steady\n",
		"current":    "v0.0.102\n",
		"quarantine": "v0.0.104\n",
	})
	if err := os.Remove(filepath.Join(dir, "pending")); err != nil {
		t.Fatal(err)
	}
	r := recordFor(e, he, mixyHost(), "v0.0.104", nil,
		&record.Sample{Packages: map[string]string{"katzenpost": "0.0.102"}}, "laptop")

	if r.RolledBack == nil {
		t.Fatal("a rollback was not recorded")
	}
	if r.RolledBack.Tag != "v0.0.104" || r.RolledBack.Why == "" {
		t.Errorf("rolled_back: %+v", r.RolledBack)
	}
	if r.Succeeded != nil {
		t.Error("a rolled-back deployment is recorded as a success")
	}
	if r.Deploying != nil {
		t.Error("a rolled-back deployment is still in flight")
	}
}

func TestATickThatDeployedNothingRecordsAnObservation(t *testing.T) {
	repo := t.TempDir()
	e := track.Env{Repo: repo, Network: "namenlos"}
	he := hostState(t, filepath.Join(t.TempDir(), "mixy"), map[string]string{
		"state": "steady\n",
	})
	now := &record.Sample{Packages: map[string]string{"katzenpost": "0.0.102"}}

	r := recordFor(e, he, mixyHost(), "v0.0.104", nil, now, "laptop")
	if r.Succeeded != nil {
		t.Errorf("a run that deployed nothing claimed a deployment: %+v", r.Succeeded)
	}
	if r.Deployed != nil {
		t.Errorf("an observation was filed as a deployment: %+v", r.Deployed)
	}
	if r.Observed == nil || r.Observed.Packages["katzenpost"] != "0.0.102" {
		t.Errorf("the reading was dropped: %+v", r.Observed)
	}
	if got := r.Summary(); got != "runs katzenpost 0.0.102, seen with no deployment" {
		t.Errorf("Summary = %q", got)
	}
}

func TestALaterTickDoesNotRestampAnOutcomeItDidNotEarn(t *testing.T) {
	repo := t.TempDir()
	e := track.Env{Repo: repo, Network: "namenlos"}
	dir := filepath.Join(t.TempDir(), "mixy")

	he := hostState(t, dir, map[string]string{
		"state": "pending\n", "current": "v0.0.104\n",
		"pending": "tag=v0.0.104\nprev=v0.0.102\n",
	})
	first := recordFor(e, he, mixyHost(), "v0.0.104", nil, nil, "laptop")
	if _, err := record.Write(repo, "namenlos", first); err != nil {
		t.Fatal(err)
	}
	he = hostState(t, dir, map[string]string{"state": "steady\n", "current": "v0.0.104\n"})
	if err := os.Remove(filepath.Join(dir, "pending")); err != nil {
		t.Fatal(err)
	}
	done := recordFor(e, he, mixyHost(), "v0.0.104", nil,
		&record.Sample{Packages: map[string]string{"katzenpost": "0.0.104"}}, "laptop")
	if _, err := record.Write(repo, "namenlos", done); err != nil {
		t.Fatal(err)
	}
	if done.Succeeded == nil || done.Succeeded.From != "v0.0.102" {
		t.Fatalf("the first outcome is wrong: %+v", done.Succeeded)
	}

	again := recordFor(e, he, mixyHost(), "v0.0.104", nil,
		&record.Sample{Packages: map[string]string{"katzenpost": "0.0.104"}}, "someone-else")
	if again.Succeeded.From != "v0.0.102" || again.Succeeded.By != "laptop" {
		t.Errorf("a later tick overwrote the outcome it did not earn: %+v", again.Succeeded)
	}
}

func TestAHostThatUpdatesItselfIsKeyedByItsOwnLabel(t *testing.T) {
	e := track.Env{
		Services: []string{"luscus-pq-authority"},
		Config: config.Config{Hosts: map[string]config.Host{
			"luscus": {Services: []string{"luscus-pq-authority"}},
		}},
	}
	if got := localLabel(e); got != "luscus" {
		t.Errorf("localLabel = %q, want luscus", got)
	}

	if got := localLabel(track.Env{Services: []string{"oneeyedman-pq-mixserver"}}); got != "oneeyedman" {
		t.Errorf("localLabel = %q, want oneeyedman", got)
	}

	if got := localLabel(track.Env{Services: []string{"a-pq-mixserver", "b-pq-mixserver"}}); got != "" {
		t.Errorf("localLabel = %q, want nothing", got)
	}
}

func TestAMachineWithNoServicesSamplesNothing(t *testing.T) {
	if got := localSample(track.Env{}, "", ""); got != nil {
		t.Errorf("localSample = %+v, want nothing", got)
	}
}

func TestTheRecordedConsensusIsTheVerdictTheTickSaw(t *testing.T) {
	r := &ToolRunner{}
	if got := r.Consensus(); got != "" {
		t.Errorf("Consensus before any verify = %q, want nothing", got)
	}
	for code, want := range map[int]string{0: "in", 1: "not yet", 2: "not yet", VerifyUnavailable: "unknown"} {
		r.note(code)
		if got := r.Consensus(); got != want {
			t.Errorf("verify %d recorded as %q, want %q", code, got, want)
		}
	}
}

func TestATickThatChangedNothingRecordsOneReading(t *testing.T) {
	repo := t.TempDir()
	e := track.Env{Repo: repo, Network: "namenlos"}
	he := hostState(t, filepath.Join(t.TempDir(), "luscus"), map[string]string{
		"state": "steady\n", "current": "v0.0.104\n",
	})
	same := func() *record.Sample {
		return &record.Sample{Packages: map[string]string{
			"katzenpost": "0.0.104", "kno": "0.0.104-130",
		}}
	}
	h := remoteHost{Label: "luscus", Node: "this host", Services: []string{"luscus-pq-authority"}}

	r := recordFor(e, he, h, "v0.0.104", same(), same(), "luscus")
	if r.Observed != nil {
		t.Errorf("the same reading was written twice:\n observed %+v\n deployed %+v", r.Observed, r.Deployed)
	}
	if r.Deployed == nil {
		t.Error("what the host is running was dropped along with the duplicate")
	}

	before := &record.Sample{Packages: map[string]string{"katzenpost": "0.0.102"}}
	r = recordFor(e, he, h, "v0.0.104", before, same(), "luscus")
	if r.Observed == nil || r.Observed.Packages["katzenpost"] != "0.0.102" {
		t.Errorf("what it was running before was lost: %+v", r.Observed)
	}
}

func TestARolledBackDeploymentReadsAsOneEndToEnd(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(t.TempDir(), "mixy")
	h := mixyHost()

	he := hostState(t, dir, map[string]string{
		"state": "pending\n", "current": "v0.0.104\n",
		"pending": "tag=v0.0.104\nprev=v0.0.102\n",
	})
	e := track.Env{Repo: repo, Network: "namenlos", QuarantineFile: filepath.Join(dir, "quarantine")}
	first := recordFor(e, he, h, "v0.0.104",
		&record.Sample{Packages: map[string]string{"katzenpost": "0.0.102"}}, nil, "laptop")
	if _, err := record.Write(repo, "namenlos", first); err != nil {
		t.Fatal(err)
	}

	he = hostState(t, dir, map[string]string{
		"state": "steady\n", "current": "v0.0.102\n", "quarantine": "v0.0.104\n",
	})
	if err := os.Remove(filepath.Join(dir, "pending")); err != nil {
		t.Fatal(err)
	}
	back := &record.Sample{
		Packages: map[string]string{"katzenpost": "0.0.102"},
		Units:    map[string]string{"pq-katzenpost-mixserver": "active"},
	}
	r := recordFor(e, he, h, "v0.0.104", nil, back, "laptop")
	if _, err := record.Write(repo, "namenlos", r); err != nil {
		t.Fatal(err)
	}

	got, err := record.Load(repo, "namenlos", "mixy")
	if err != nil {
		t.Fatal(err)
	}
	if got.RolledBack == nil {
		t.Fatal("a rollback reads as an ordinary host on an older release")
	}
	if got.RolledBack.Tag != "v0.0.104" || got.RolledBack.From != "v0.0.102" {
		t.Errorf("rolled_back = %+v", got.RolledBack)
	}
	if got.RolledBack.Why == "" {
		t.Error("it does not say why it was rolled back")
	}
	if got.Succeeded != nil {
		t.Errorf("it also claims success: %+v", got.Succeeded)
	}
	if got.Deploying != nil {
		t.Error("it is still recorded as in flight")
	}
	if got.Deployed == nil || got.Deployed.Packages["katzenpost"] != "0.0.102" {
		t.Errorf("what the host is running now: %+v", got.Deployed)
	}
	if !strings.Contains(got.Summary(), "rolled back") {
		t.Errorf("Summary = %q, want it to say a rollback happened", got.Summary())
	}
}

func TestAHostNothingCouldConfirmDoesNotReadAsHealthy(t *testing.T) {
	repo, dir := t.TempDir(), t.TempDir()
	e := track.Env{Repo: repo, Network: "namenlos"}
	he := hostState(t, dir, map[string]string{"state": "steady\n", "current": "v0.0.104\n"})

	after := &record.Sample{
		Packages: map[string]string{"katzenpost": "0.0.104"},
		Verified: "verify could not run",
	}
	r := recordFor(e, he, mixyHost(), "v0.0.104", nil, after, "laptop")
	if r.Succeeded == nil {
		t.Fatal("the packages are installed; the outcome should say so")
	}
	if r.Succeeded.Verified != "verify could not run" {
		t.Errorf("the outcome lost the verdict: %+v", r.Succeeded)
	}
	got := r.Summary()
	if !strings.Contains(got, "last verify:") {
		t.Errorf("a host nothing could confirm reads as healthy: %q", got)
	}

	ok := recordFor(e, he, mixyHost(), "v0.0.104", nil, &record.Sample{
		Packages: map[string]string{"katzenpost": "0.0.104"},
		Verified: record.VerifiedOK,
	}, "laptop")
	if s := ok.Summary(); strings.Contains(s, "last verify:") {
		t.Errorf("a verified host reads as suspect: %q", s)
	}
}

func TestANoOpTickDoesNotEraseTheLastVerification(t *testing.T) {
	repo := t.TempDir()
	e := track.Env{Repo: repo, Network: "namenlos"}
	dir := filepath.Join(t.TempDir(), "mixy")
	pkgs := map[string]string{"katzenpost": "0.0.104"}

	he := hostState(t, dir, map[string]string{"state": "steady\n", "current": "v0.0.104\n"})
	verified := time.Now().UTC().Add(-2 * time.Hour)
	done := recordFor(e, he, mixyHost(), "v0.0.104",
		&record.Sample{Packages: map[string]string{"katzenpost": "0.0.102"}},
		&record.Sample{
			Sampled: verified, Packages: pkgs,
			Consensus: "in", Verified: record.VerifiedOK, VerifiedAt: verified,
		}, "laptop")
	if _, err := record.Write(repo, "namenlos", done); err != nil {
		t.Fatal(err)
	}

	he = hostState(t, dir, map[string]string{"state": "steady\n", "current": "v0.0.104\n"})
	now := time.Now().UTC()
	bare := &record.Sample{Sampled: now, Packages: pkgs}
	again := recordFor(e, he, mixyHost(), "v0.0.104",
		&record.Sample{Sampled: now, Packages: pkgs}, bare, "laptop")

	kept := again.Deployed
	if kept == nil {
		kept = again.Observed
	}
	if kept == nil {
		t.Fatal("the no-op tick kept no reading at all")
	}
	if kept.Verified != record.VerifiedOK || kept.Consensus != "in" {
		t.Errorf("a no-op tick blanked the verification: consensus=%q verified=%q",
			kept.Consensus, kept.Verified)
	}

	if !kept.VerifiedAt.Equal(verified) {
		t.Errorf("VerifiedAt = %v, want the hour it was actually verified (%v)",
			kept.VerifiedAt, verified)
	}
}

func TestAFreshVerdictOverridesTheStoredOne(t *testing.T) {
	stored := &record.Sample{Consensus: "in", Verified: record.VerifiedOK,
		VerifiedAt: time.Now().UTC().Add(-time.Hour)}
	fresh := &record.Sample{Consensus: "not yet", Verified: "failed",
		VerifiedAt: time.Now().UTC()}
	got := carryVerification(fresh, stored)
	if got.Verified != "failed" || got.Consensus != "not yet" {
		t.Errorf("a stored pass overrode a fresh failure: %+v", got)
	}
}

func TestARollbackIsReadFromTheQuarantineOfItsHost(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(t.TempDir(), "mixy")
	e := track.Env{Repo: repo, Network: "namenlos", QuarantineFile: filepath.Join(t.TempDir(), "quarantine")}
	he := hostState(t, dir, map[string]string{"state": "pending\n", "current": "v0.0.104\n",
		"pending": "tag=v0.0.104\nprev=v0.0.102\n"})
	first := recordFor(e, he, mixyHost(), "v0.0.104",
		&record.Sample{Packages: map[string]string{"katzenpost": "0.0.102"}}, nil, "laptop")
	if _, err := record.Write(repo, "namenlos", first); err != nil {
		t.Fatal(err)
	}
	he = hostState(t, dir, map[string]string{"state": "steady\n", "current": "v0.0.102\n",
		"quarantine": "v0.0.104\n"})
	if err := os.Remove(filepath.Join(dir, "pending")); err != nil {
		t.Fatal(err)
	}
	after := &record.Sample{Packages: map[string]string{"katzenpost": "0.0.102"}, Verified: record.VerifiedFailed}
	r := recordFor(e, he, mixyHost(), "v0.0.104", nil, after, "laptop")
	if r.RolledBack == nil {
		t.Fatal("a rollback that only the host's own quarantine shows was not recorded")
	}
	if !strings.Contains(r.RolledBack.Why, "exit 2") {
		t.Errorf("a rollback after verify failed says why = %q", r.RolledBack.Why)
	}
}
