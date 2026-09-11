package update

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/track"
	"github.com/katzenpost/kno/internal/verify"
)

var testNow = time.Unix(1600000000, 0)

type fake struct {
	calls       []string
	trackTag    string
	trackRC     int
	buildErr    error
	activateErr map[string]error
	verifyRC    []int

	configs string
}

func (f *fake) Configs() string { return f.configs }

func (f *fake) Track() (string, int) {
	f.calls = append(f.calls, "track")
	return f.trackTag, f.trackRC
}

func (f *fake) Build(tag string) error {
	f.calls = append(f.calls, "build "+tag)
	return f.buildErr
}

func (f *fake) Activate(tag, rel string) error {
	f.calls = append(f.calls, "activate "+tag+" "+rel)
	return f.activateErr[tag]
}

func (f *fake) Verify(rel string, consensusOnly bool) int {
	f.calls = append(f.calls, fmt.Sprintf("verify %q consensus-only=%v", rel, consensusOnly))
	if len(f.verifyRC) == 0 {
		return 0
	}
	rc := f.verifyRC[0]
	f.verifyRC = f.verifyRC[1:]
	return rc
}

func newEnv(t *testing.T) track.Env {
	t.Helper()
	home := t.TempDir()
	e := track.Env{
		Home:        home,
		StateDir:    filepath.Join(home, "state"),
		ReleasesDir: filepath.Join(home, "releases"),
		Network:     "namenlos",
	}
	e.QuarantineFile = filepath.Join(e.StateDir, "quarantine")
	e.CurrentFile = filepath.Join(e.StateDir, "current")
	return e
}

func mkRelease(t *testing.T, e track.Env, tags ...string) {
	t.Helper()
	for _, tag := range tags {
		if err := os.MkdirAll(filepath.Join(e.ReleasesDir, tag, "manifest"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func opts(e track.Env, f *fake, out *strings.Builder) Options {
	return Options{
		Env:    e,
		Policy: "apply",
		Now:    func() time.Time { return testNow },
		Runner: f,
		Out:    out,
	}
}

func wantCalls(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls\n got: %s\nwant: %s", strings.Join(got, " | "), strings.Join(want, " | "))
	}
}

func wantFile(t *testing.T, path, want string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", filepath.Base(path), err)
	}
	if got := strings.TrimRight(string(b), "\n"); got != want {
		t.Errorf("%s = %q, want %q", filepath.Base(path), got, want)
	}
}

func wantAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("%s should not exist", filepath.Base(path))
	}
}

func TestSteadyParksTheNewReleaseInPending(t *testing.T) {
	e := newEnv(t)
	f := &fake{trackTag: "v0.0.2"}
	var out strings.Builder
	if rc := Run(opts(e, f, &out)); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, out.String())
	}
	rel := filepath.Join(e.ReleasesDir, "v0.0.2")
	wantCalls(t, f.calls, []string{"track", "build v0.0.2", "activate v0.0.2 " + rel})
	wantFile(t, e.CurrentFile, "v0.0.2")
	wantFile(t, filepath.Join(e.StateDir, "state"), "pending")
	wantAbsent(t, filepath.Join(e.StateDir, "last-known-good"))
	deadline := testNow.Add(DefaultWindow).Unix()

	wantFile(t, filepath.Join(e.StateDir, "pending"),
		fmt.Sprintf("tag=v0.0.2\nprev=\nactivated=%d\npromote=%d\ndeadline=%d\nconfigs=\nprevconfigs=",
			testNow.Unix(), testNow.Add(promoteFloor()).Unix(), deadline))
}

func TestNoBaselineConsensusDoesNotBlockTheDeploy(t *testing.T) {
	e := newEnv(t)
	if err := os.MkdirAll(e.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, e.CurrentFile, "v0.0.1\n")
	f := &fake{trackTag: "v0.0.2", verifyRC: []int{1}}
	var out strings.Builder
	if rc := Run(opts(e, f, &out)); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, out.String())
	}

	for _, want := range []string{"build v0.0.2", "activate v0.0.2"} {
		var found bool
		for _, c := range f.calls {
			if strings.HasPrefix(c, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("consensus blocked the deploy: %q is not in %v", want, f.calls)
		}
	}

	if !strings.Contains(out.String(), "no consensus before the install") {
		t.Errorf("the missing baseline was not reported:\n%s", out.String())
	}
	if strings.Contains(out.String(), "deferring upgrade") {
		t.Errorf("still defers on a consensus verdict:\n%s", out.String())
	}
}

func TestPreflightFailureShowsWhatTheChildSaid(t *testing.T) {
	var out strings.Builder
	r := ToolRunner{
		VerifyCmd: []string{"sh", "-c",
			`echo "cannot check consensus: /etc/kpclientd/thinclient.toml does not exist" >&2; exit 1`},
		Log: track.Logger{Tool: "update", W: &out},
		Out: &out,
	}
	if rc := r.Verify("", true); rc != 1 {
		t.Fatalf("rc = %d, want 1\n%s", rc, out.String())
	}
	for _, want := range []string{"consensus check before the install failed", "thinclient.toml does not exist"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}

	var quiet strings.Builder
	q := ToolRunner{
		VerifyCmd: []string{"sh", "-c", `echo chatty; echo noisy >&2; exit 0`},
		Log:       track.Logger{Tool: "update", W: &quiet},
		Out:       &quiet,
	}
	if rc := q.Verify("", true); rc != 0 {
		t.Fatalf("rc = %d, want 0\n%s", rc, quiet.String())
	}
	if quiet.Len() != 0 {
		t.Errorf("a passing pre-flight is no longer quiet:\n%s", quiet.String())
	}
}

func TestSteadyFailsWhenTheVerifyToolCannotRun(t *testing.T) {
	e := newEnv(t)
	if err := os.MkdirAll(e.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, e.CurrentFile, "v0.0.1\n")
	f := &fake{trackTag: "v0.0.2", verifyRC: []int{VerifyUnavailable}}
	var out strings.Builder
	if rc := Run(opts(e, f, &out)); rc != 1 {
		t.Fatalf("rc = %d, want 1\n%s", rc, out.String())
	}
	wantCalls(t, f.calls, []string{"track", `verify "" consensus-only=true`})
	wantFile(t, e.CurrentFile, "v0.0.1")
	wantAbsent(t, filepath.Join(e.StateDir, "state"))
	if !strings.Contains(out.String(), "kno could not run verify") {
		t.Errorf("missing the could-not-run line:\n%s", out.String())
	}
}

func TestSteadyRecordsThePreviousVersionAsLastKnownGood(t *testing.T) {
	e := newEnv(t)
	mkRelease(t, e, "v0.0.1")
	if err := os.MkdirAll(e.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, e.CurrentFile, "v0.0.1\n")
	f := &fake{trackTag: "v0.0.2", verifyRC: []int{0}}
	var out strings.Builder
	if rc := Run(opts(e, f, &out)); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, out.String())
	}
	wantCalls(t, f.calls, []string{
		"track",
		`verify "" consensus-only=true`,
		"build v0.0.2",
		"activate v0.0.2 " + filepath.Join(e.ReleasesDir, "v0.0.2"),
	})
	wantFile(t, filepath.Join(e.StateDir, "last-known-good"), "v0.0.1")
	wantFile(t, e.CurrentFile, "v0.0.2")
}

func TestNothingToDoWhenAlreadyAtTheTarget(t *testing.T) {
	e := newEnv(t)
	f := &fake{trackTag: "v0.0.2", trackRC: 3}
	var out strings.Builder
	if rc := Run(opts(e, f, &out)); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	wantCalls(t, f.calls, []string{"track"})
	if strings.Contains(out.String(), "nothing to do") {
		t.Errorf("the session repeats the nothing-to-do line that Track already printed:\n%s", out.String())
	}
}

func TestAFailedDeliveryDoesNotBurnTheRelease(t *testing.T) {
	e := newEnv(t)
	mkRelease(t, e, "v0.0.1")
	if err := os.MkdirAll(e.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, e.CurrentFile, "v0.0.1\n")
	f := &fake{
		trackTag:    "v0.0.2",
		verifyRC:    []int{0},
		activateErr: map[string]error{"v0.0.2": fmt.Errorf("dpkg failed")},
	}
	var out strings.Builder

	if rc := Run(opts(e, f, &out)); rc == 0 {
		t.Fatalf("a failed install reported success\n%s", out.String())
	}
	wantCalls(t, f.calls, []string{
		"track",
		`verify "" consensus-only=true`,
		"build v0.0.2",
		"activate v0.0.2 " + filepath.Join(e.ReleasesDir, "v0.0.2"),
		"activate v0.0.1 " + filepath.Join(e.ReleasesDir, "v0.0.1"),
	})
	wantAbsent(t, e.QuarantineFile)
	wantFile(t, e.CurrentFile, "v0.0.1")
	wantFile(t, filepath.Join(e.StateDir, "state"), "steady")
	wantAbsent(t, filepath.Join(e.StateDir, "pending"))
	if !strings.Contains(out.String(), "did not quarantine v0.0.2") {
		t.Errorf("the run does not say the release is still usable:\n%s", out.String())
	}
}

func TestAReleaseThatFailedVerificationIsQuarantined(t *testing.T) {
	e := newEnv(t)
	mkRelease(t, e, "v0.0.1")
	mkRelease(t, e, "v0.0.2")
	if err := os.MkdirAll(e.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, e.CurrentFile, "v0.0.2\n")
	write(t, filepath.Join(e.StateDir, "last-known-good"), "v0.0.1\n")
	write(t, filepath.Join(e.StateDir, "pending"), "tag=v0.0.2\nprev=v0.0.1\ndeadline=1\n")
	write(t, filepath.Join(e.StateDir, "state"), "pending\n")

	f := &fake{verifyRC: []int{2}}
	var out strings.Builder
	Run(opts(e, f, &out))
	wantFile(t, e.QuarantineFile, "v0.0.2")
	wantFile(t, e.CurrentFile, "v0.0.1")
	if !strings.Contains(out.String(), "quarantined v0.0.2") {
		t.Errorf("the rollback did not say that it quarantined v0.0.2:\n%s", out.String())
	}
}

func TestPendingPromotesAfterVerification(t *testing.T) {
	e := newEnv(t)
	mkRelease(t, e, "v0.0.1", "v0.0.2")
	write(t, filepath.Join(e.StateDir, "state"), "pending\n")
	write(t, filepath.Join(e.StateDir, "pending"),
		fmt.Sprintf("tag=v0.0.2\nprev=v0.0.1\ndeadline=%d\n", testNow.Unix()+60))
	f := &fake{verifyRC: []int{0}}
	var out strings.Builder
	if rc := Run(opts(e, f, &out)); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, out.String())
	}
	wantCalls(t, f.calls, []string{
		fmt.Sprintf("verify %q consensus-only=false", filepath.Join(e.ReleasesDir, "v0.0.2")),
	})
	wantFile(t, filepath.Join(e.StateDir, "last-known-good"), "v0.0.2")
	wantFile(t, filepath.Join(e.StateDir, "state"), "steady")
	wantAbsent(t, filepath.Join(e.StateDir, "pending"))
	wantAbsent(t, e.QuarantineFile)
}

func TestPendingRollsBackOnABrokenNode(t *testing.T) {
	e := newEnv(t)
	mkRelease(t, e, "v0.0.1", "v0.0.2")
	write(t, filepath.Join(e.StateDir, "state"), "pending\n")
	write(t, filepath.Join(e.StateDir, "pending"),
		fmt.Sprintf("tag=v0.0.2\nprev=v0.0.1\ndeadline=%d\n", testNow.Unix()+3600))
	f := &fake{verifyRC: []int{2}}
	var out strings.Builder
	if rc := Run(opts(e, f, &out)); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, out.String())
	}
	wantCalls(t, f.calls, []string{
		fmt.Sprintf("verify %q consensus-only=false", filepath.Join(e.ReleasesDir, "v0.0.2")),
		"activate v0.0.1 " + filepath.Join(e.ReleasesDir, "v0.0.1"),
	})
	wantFile(t, e.QuarantineFile, "v0.0.2")
	wantFile(t, e.CurrentFile, "v0.0.1")
	wantFile(t, filepath.Join(e.StateDir, "state"), "steady")
}

func TestRollbackActivationFailureKeepsTheHonestCurrent(t *testing.T) {
	e := newEnv(t)
	mkRelease(t, e, "v0.0.1", "v0.0.2")
	write(t, filepath.Join(e.StateDir, "state"), "pending\n")
	write(t, e.CurrentFile, "v0.0.2\n")
	deadline := testNow.Unix()
	write(t, filepath.Join(e.StateDir, "pending"),
		fmt.Sprintf("tag=v0.0.2\nprev=v0.0.1\ndeadline=%d\n", deadline))
	f := &fake{
		verifyRC:    []int{1},
		activateErr: map[string]error{"v0.0.1": fmt.Errorf("dpkg was held")},
	}
	var out strings.Builder
	if rc := Run(opts(e, f, &out)); rc != 1 {
		t.Fatalf("rc = %d, want 1\n%s", rc, out.String())
	}
	wantCalls(t, f.calls, []string{
		fmt.Sprintf("verify %q consensus-only=false", filepath.Join(e.ReleasesDir, "v0.0.2")),
		"activate v0.0.1 " + filepath.Join(e.ReleasesDir, "v0.0.1"),
	})
	wantFile(t, e.CurrentFile, "v0.0.2")
	wantAbsent(t, e.QuarantineFile)
	wantFile(t, filepath.Join(e.StateDir, "state"), "pending")
	wantFile(t, filepath.Join(e.StateDir, "pending"),
		fmt.Sprintf("tag=v0.0.2\nprev=v0.0.1\ndeadline=%d\nverdict=1\nchecked=%d", deadline, testNow.Unix()))
	if !strings.Contains(out.String(), "The host runs v0.0.2") {
		t.Errorf("the failure does not name the release the host is on:\n%s", out.String())
	}
}

func TestPendingWaitsUntilTheDeadline(t *testing.T) {
	e := newEnv(t)
	mkRelease(t, e, "v0.0.1", "v0.0.2")
	write(t, filepath.Join(e.StateDir, "state"), "pending\n")
	deadline := testNow.Unix() + 60
	write(t, filepath.Join(e.StateDir, "pending"),
		fmt.Sprintf("tag=v0.0.2\nprev=v0.0.1\ndeadline=%d\n", deadline))
	f := &fake{verifyRC: []int{1}}
	var out strings.Builder
	if rc := Run(opts(e, f, &out)); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, out.String())
	}
	wantCalls(t, f.calls, []string{
		fmt.Sprintf("verify %q consensus-only=false", filepath.Join(e.ReleasesDir, "v0.0.2")),
	})
	wantFile(t, filepath.Join(e.StateDir, "state"), "pending")
	if !strings.Contains(out.String(), "waiting on v0.0.2: the verify window is open until "+clock(deadline)) {
		t.Errorf("missing the waiting line:\n%s", out.String())
	}
}

func TestPendingHoldsTheTagWhenTheVerifyToolCannotRun(t *testing.T) {
	e := newEnv(t)
	mkRelease(t, e, "v0.0.1", "v0.0.2")
	write(t, filepath.Join(e.StateDir, "state"), "pending\n")
	write(t, e.CurrentFile, "v0.0.2\n")
	write(t, filepath.Join(e.StateDir, "pending"),
		fmt.Sprintf("tag=v0.0.2\nprev=v0.0.1\ndeadline=%d\n", testNow.Unix()))
	f := &fake{verifyRC: []int{VerifyUnavailable}}
	var out strings.Builder
	if rc := Run(opts(e, f, &out)); rc != 1 {
		t.Fatalf("rc = %d, want 1\n%s", rc, out.String())
	}
	wantCalls(t, f.calls, []string{
		fmt.Sprintf("verify %q consensus-only=false", filepath.Join(e.ReleasesDir, "v0.0.2")),
	})
	wantFile(t, e.CurrentFile, "v0.0.2")
	wantAbsent(t, e.QuarantineFile)
	wantFile(t, filepath.Join(e.StateDir, "state"), "pending")
	if !strings.Contains(out.String(), "kno could not run verify") {
		t.Errorf("missing the could-not-run line:\n%s", out.String())
	}
}

func TestPendingRollsBackToLastKnownGoodAtTheDeadline(t *testing.T) {
	e := newEnv(t)
	mkRelease(t, e, "v0.0.0", "v0.0.2")
	write(t, filepath.Join(e.StateDir, "state"), "pending\n")
	write(t, filepath.Join(e.StateDir, "last-known-good"), "v0.0.0\n")
	write(t, filepath.Join(e.StateDir, "pending"),
		fmt.Sprintf("tag=v0.0.2\nprev=v0.0.1\ndeadline=%d\n", testNow.Unix()))
	f := &fake{verifyRC: []int{1}}
	var out strings.Builder
	if rc := Run(opts(e, f, &out)); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, out.String())
	}
	wantCalls(t, f.calls, []string{
		fmt.Sprintf("verify %q consensus-only=false", filepath.Join(e.ReleasesDir, "v0.0.2")),
		"activate v0.0.0 " + filepath.Join(e.ReleasesDir, "v0.0.0"),
	})
	wantFile(t, e.CurrentFile, "v0.0.0")
	wantFile(t, e.QuarantineFile, "v0.0.2")
}

func TestStallForwardHoldsAtTheDeadline(t *testing.T) {
	e := newEnv(t)
	mkRelease(t, e, "v0.0.1", "v0.0.2")
	write(t, filepath.Join(e.StateDir, "state"), "pending\n")
	write(t, filepath.Join(e.StateDir, "pending"),
		fmt.Sprintf("tag=v0.0.2\nprev=v0.0.1\ndeadline=%d\n", testNow.Unix()))
	f := &fake{verifyRC: []int{1}}
	var out strings.Builder
	o := opts(e, f, &out)
	o.StallForward = true
	if rc := Run(o); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, out.String())
	}
	wantCalls(t, f.calls, []string{
		fmt.Sprintf("verify %q consensus-only=false", filepath.Join(e.ReleasesDir, "v0.0.2")),
	})
	wantFile(t, filepath.Join(e.StateDir, "state"), "pending")
	if kv, _ := readKV(filepath.Join(e.StateDir, "pending")); kv["held"] != "1" {
		t.Errorf("the stalled host is not held: %v", kv)
	}
	wantAbsent(t, e.QuarantineFile)
}

func TestAVerdictAboutTheNetworkDoesNotBurnTheTag(t *testing.T) {
	for _, c := range []struct {
		name string
		rc   int
		want string
	}{
		{"the network has no consensus", verify.CodeNoConsensus, "no consensus (exit 3)"},
		{"this host could not ask", verify.CodeCannotAsk, "cannot ask (exit 4)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			mkRelease(t, e, "v0.0.1", "v0.0.2")
			write(t, filepath.Join(e.StateDir, "state"), "pending\n")
			write(t, e.CurrentFile, "v0.0.2\n")
			write(t, filepath.Join(e.StateDir, "pending"),
				fmt.Sprintf("tag=v0.0.2\nprev=v0.0.1\ndeadline=%d\n", testNow.Unix()))
			f := &fake{verifyRC: []int{c.rc}}
			var out strings.Builder
			if rc := Run(opts(e, f, &out)); rc != 0 {
				t.Fatalf("rc = %d\n%s", rc, out.String())
			}

			wantCalls(t, f.calls, []string{
				fmt.Sprintf("verify %q consensus-only=false", filepath.Join(e.ReleasesDir, "v0.0.2")),
			})
			wantFile(t, e.CurrentFile, "v0.0.2")
			wantAbsent(t, e.QuarantineFile)
			wantFile(t, filepath.Join(e.StateDir, "state"), "pending")
			kv, _ := readKV(filepath.Join(e.StateDir, "pending"))
			if kv["held"] != "1" || kv["tag"] != "v0.0.2" {
				t.Errorf("the host is not held on v0.0.2: %v", kv)
			}
			wantAbsent(t, filepath.Join(e.StateDir, "last-known-good"))

			if !strings.Contains(out.String(), c.want) {
				t.Errorf("the hold does not name the verdict %q:\n%s", c.want, out.String())
			}
		})
	}
}

func heldEnv(t *testing.T) track.Env {
	t.Helper()
	e := newEnv(t)
	mkRelease(t, e, "v0.0.1", "v0.0.2", "v0.0.3")
	write(t, filepath.Join(e.StateDir, "state"), "pending\n")
	write(t, e.CurrentFile, "v0.0.2\n")
	write(t, filepath.Join(e.StateDir, "last-known-good"), "v0.0.1\n")
	write(t, filepath.Join(e.StateDir, "pending"),
		fmt.Sprintf("tag=v0.0.2\nprev=v0.0.1\nactivated=%d\ndeadline=%d\nheld=1\n",
			testNow.Unix()-7200, testNow.Unix()-1800))
	return e
}

func TestAHeldHostIsPromotedWhenVerifyPasses(t *testing.T) {
	e := heldEnv(t)
	f := &fake{verifyRC: []int{0}}
	var out strings.Builder
	if rc := Run(opts(e, f, &out)); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, out.String())
	}
	wantFile(t, filepath.Join(e.StateDir, "state"), "steady")
	wantFile(t, filepath.Join(e.StateDir, "last-known-good"), "v0.0.2")
	wantAbsent(t, filepath.Join(e.StateDir, "pending"))
}

func TestAHeldHostStaysHeldWhileVerifyCannotJudge(t *testing.T) {
	for _, rc := range []int{verify.CodeNoConsensus, verify.CodeCannotAsk} {
		e := heldEnv(t)
		f := &fake{verifyRC: []int{rc}}
		var out strings.Builder
		if got := Run(opts(e, f, &out)); got != 0 {
			t.Fatalf("rc = %d\n%s", got, out.String())
		}
		wantFile(t, filepath.Join(e.StateDir, "state"), "pending")
		kv, _ := readKV(filepath.Join(e.StateDir, "pending"))
		if kv["held"] != "1" || kv["verdict"] != fmt.Sprint(rc) {
			t.Errorf("verdict %d: pending = %v", rc, kv)
		}
		wantFile(t, e.CurrentFile, "v0.0.2")
		wantAbsent(t, e.QuarantineFile)
		if !strings.Contains(out.String(), "v0.0.2 is held") {
			t.Errorf("verdict %d: the held line is missing:\n%s", rc, out.String())
		}
	}
}

func TestAHeldHostGetsANewWindowWhenVerifyCanJudgeAgain(t *testing.T) {
	e := heldEnv(t)
	f := &fake{verifyRC: []int{1}}
	var out strings.Builder
	if rc := Run(opts(e, f, &out)); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, out.String())
	}
	wantFile(t, filepath.Join(e.StateDir, "state"), "pending")
	kv, _ := readKV(filepath.Join(e.StateDir, "pending"))
	want := fmt.Sprint(testNow.Add(DefaultWindow).Unix())
	if kv["held"] != "" || kv["deadline"] != want {
		t.Errorf("pending = %v, want no hold and deadline %s", kv, want)
	}
	wantAbsent(t, e.QuarantineFile)
	wantFile(t, e.CurrentFile, "v0.0.2")
}

func TestAHeldHostRollsBackWhenVerifyFails(t *testing.T) {
	e := heldEnv(t)
	f := &fake{verifyRC: []int{2}}
	var out strings.Builder
	Run(opts(e, f, &out))
	wantFile(t, e.CurrentFile, "v0.0.1")
	wantFile(t, e.QuarantineFile, "v0.0.2")
	wantFile(t, filepath.Join(e.StateDir, "state"), "steady")
}

func TestAHeldHostMovesOnToANewerTarget(t *testing.T) {
	e := heldEnv(t)
	f := &fake{verifyRC: []int{verify.CodeNoConsensus}, trackTag: "v0.0.3"}
	var out strings.Builder
	o := opts(e, f, &out)
	o.Target = func() string { return "v0.0.3" }
	if rc := Run(o); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, out.String())
	}
	wantFile(t, e.CurrentFile, "v0.0.3")
	kv, _ := readKV(filepath.Join(e.StateDir, "pending"))
	if kv["tag"] != "v0.0.3" || kv["held"] != "" || kv["prev"] != "v0.0.1" {
		t.Errorf("pending = %v, want a new deployment of v0.0.3 from v0.0.1", kv)
	}
	wantFile(t, filepath.Join(e.StateDir, "last-known-good"), "v0.0.1")
}

func TestAVerdictAboutTheNodeStillRollsBack(t *testing.T) {
	e := newEnv(t)
	mkRelease(t, e, "v0.0.1", "v0.0.2")
	write(t, filepath.Join(e.StateDir, "state"), "pending\n")
	write(t, e.CurrentFile, "v0.0.2\n")
	write(t, filepath.Join(e.StateDir, "pending"),
		fmt.Sprintf("tag=v0.0.2\nprev=v0.0.1\ndeadline=%d\n", testNow.Unix()))

	f := &fake{verifyRC: []int{verify.CodeNotYet}}
	var out strings.Builder
	if rc := Run(opts(e, f, &out)); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, out.String())
	}
	wantCalls(t, f.calls, []string{
		fmt.Sprintf("verify %q consensus-only=false", filepath.Join(e.ReleasesDir, "v0.0.2")),
		"activate v0.0.1 " + filepath.Join(e.ReleasesDir, "v0.0.1"),
	})
	wantFile(t, e.CurrentFile, "v0.0.1")
	wantFile(t, e.QuarantineFile, "v0.0.2")
}

func TestABrokenNodeRollsBackEvenWithNoNetworkConsensus(t *testing.T) {
	e := newEnv(t)
	mkRelease(t, e, "v0.0.1", "v0.0.2")
	write(t, filepath.Join(e.StateDir, "state"), "pending\n")
	write(t, e.CurrentFile, "v0.0.2\n")
	write(t, filepath.Join(e.StateDir, "pending"),
		fmt.Sprintf("tag=v0.0.2\nprev=v0.0.1\ndeadline=%d\n", testNow.Unix()+3600))
	f := &fake{verifyRC: []int{verify.CodeFailed}}
	var out strings.Builder
	if rc := Run(opts(e, f, &out)); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, out.String())
	}
	wantFile(t, e.CurrentFile, "v0.0.1")
	wantFile(t, e.QuarantineFile, "v0.0.2")
}

func TestPendingWithoutAPendingFileReturnsToSteady(t *testing.T) {
	e := newEnv(t)
	write(t, filepath.Join(e.StateDir, "state"), "pending\n")
	f := &fake{}
	var out strings.Builder
	if rc := Run(opts(e, f, &out)); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, out.String())
	}
	if len(f.calls) != 0 {
		t.Errorf("nothing should have been driven, got %v", f.calls)
	}
	wantFile(t, filepath.Join(e.StateDir, "state"), "steady")
}

func TestPolicyOffDoesNothing(t *testing.T) {
	e := newEnv(t)
	f := &fake{trackTag: "v0.0.2"}
	var out strings.Builder
	o := opts(e, f, &out)
	o.Policy = "off"
	if rc := Run(o); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	if len(f.calls) != 0 {
		t.Errorf("policy off drove %v", f.calls)
	}
}

func TestPolicySimulateNeitherBuildsNorActivates(t *testing.T) {
	e := newEnv(t)
	if err := os.MkdirAll(e.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, e.CurrentFile, "v0.0.1\n")
	f := &fake{trackTag: "v0.0.2", verifyRC: []int{0}}
	var out strings.Builder
	o := opts(e, f, &out)
	o.Policy = "simulate"
	if rc := Run(o); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, out.String())
	}
	wantCalls(t, f.calls, []string{"track", `verify "" consensus-only=true`})
	wantFile(t, e.CurrentFile, "v0.0.1")
	wantAbsent(t, filepath.Join(e.StateDir, "state"))
	for _, must := range []string{"DRYRUN: build v0.0.2", "DRYRUN: activate "} {
		if !strings.Contains(out.String(), must) {
			t.Errorf("missing %q in:\n%s", must, out.String())
		}
	}
}

func TestUnknownPolicyIsRefused(t *testing.T) {
	e := newEnv(t)
	f := &fake{trackTag: "v0.0.2"}
	var out strings.Builder
	o := opts(e, f, &out)
	o.Policy = "sometimes"
	if rc := Run(o); rc != 1 {
		t.Fatalf("rc = %d, want 1", rc)
	}
	if len(f.calls) != 0 {
		t.Errorf("an unknown policy drove %v", f.calls)
	}
}

func TestToolRunnerActivateDoesNotPassACrossBuildTarget(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "child-env")
	script := filepath.Join(dir, "activate")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nenv > "+envFile+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KNO_TARGET_ARCH", "arm64")

	var out strings.Builder
	r := ToolRunner{
		ActivateCmd: []string{script},
		Log:         track.Logger{Tool: "update", W: &out},
		Out:         &out,
	}
	if err := r.Activate("v0.0.2", filepath.Join(dir, "releases", "v0.0.2")); err != nil {
		t.Fatalf("Activate: %v\n%s", err, out.String())
	}
	b, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "KNO_TARGET_ARCH") {
		t.Errorf("the cross-build target reached the activate child:\n%s", b)
	}
}

func TestToolRunnerVerifyTellsACrashApartFromANo(t *testing.T) {
	dir := t.TempDir()
	script := func(code int) string {
		p := filepath.Join(dir, fmt.Sprintf("verify-%d", code))
		if err := os.WriteFile(p, []byte(fmt.Sprintf("#!/bin/sh\nexit %d\n", code)), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, tc := range []struct {
		name string
		cmd  []string
		want int
	}{
		{"healthy", []string{script(0)}, 0},
		{"not in consensus yet", []string{script(1)}, 1},
		{"broken node", []string{script(2)}, 2},
		{"missing tool", []string{filepath.Join(dir, "not-installed")}, VerifyUnavailable},
		{"nothing configured", nil, VerifyUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			r := ToolRunner{
				VerifyCmd: tc.cmd,
				Log:       track.Logger{Tool: "update", W: &out},
				Out:       &out,
			}
			if got := r.Verify("", false); got != tc.want {
				t.Fatalf("Verify = %d, want %d\n%s", got, tc.want, out.String())
			}
		})
	}
}

type trackReal struct {
	*fake
	real ToolRunner
}

func (t trackReal) Track() (string, int) {
	t.fake.calls = append(t.fake.calls, "track")
	return t.real.Track()
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "core.hooksPath=/dev/null", "-c", "user.name=kno-test", "-c", "user.email=kno@test"}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func initRepo(t *testing.T, dir, tag string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q")
	git(t, dir, "commit", "--allow-empty", "-q", "-m", "root")
	git(t, dir, "tag", tag)
}

func TestRefusesQuarantinedAndUnsignedTags(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	e := newEnv(t)
	e.Repo = filepath.Join(e.Home, "network-organizer")
	e.KatzenpostRepo = filepath.Join(e.Home, "katzenpost")
	initRepo(t, e.Repo, "v0.0.2")
	initRepo(t, e.KatzenpostRepo, "v0.0.2")
	vp := filepath.Join(e.Repo, track.VersionsFile)
	if err := os.MkdirAll(filepath.Dir(vp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vp, []byte("v0.0.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(t *testing.T, e track.Env) (int, *fake) {
		t.Helper()
		f := &fake{}
		var out strings.Builder
		o := opts(e, f, &out)
		o.Runner = trackReal{fake: f, real: ToolRunner{
			Env: e,
			Log: track.Logger{Tool: "update", W: &out},
			Out: &out,
		}}
		return Run(o), f
	}

	t.Run("resolves", func(t *testing.T) {
		rc, f := run(t, e)
		if rc != 0 {
			t.Fatalf("rc = %d, want 0", rc)
		}
		wantCalls(t, f.calls, []string{
			"track", "build v0.0.2", "activate v0.0.2 " + filepath.Join(e.ReleasesDir, "v0.0.2"),
		})
	})

	t.Run("quarantined", func(t *testing.T) {
		q := newEnv(t)
		q.Repo, q.KatzenpostRepo = e.Repo, e.KatzenpostRepo
		write(t, q.QuarantineFile, "v0.0.2\n")
		rc, f := run(t, q)
		if rc != 1 {
			t.Fatalf("rc = %d, want 1", rc)
		}
		wantCalls(t, f.calls, []string{"track"})
	})

	t.Run("unsigned", func(t *testing.T) {
		u := newEnv(t)
		u.Repo, u.KatzenpostRepo = e.Repo, e.KatzenpostRepo
		u.VerifyTag = true
		rc, f := run(t, u)
		if rc != 1 {
			t.Fatalf("rc = %d, want 1", rc)
		}
		wantCalls(t, f.calls, []string{"track"})
	})
}

func TestTheOnlyReleaseAHostHasIsNotQuarantined(t *testing.T) {
	e := newEnv(t)
	mkRelease(t, e, "v0.0.2")
	if err := os.MkdirAll(e.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, e.CurrentFile, "v0.0.2\n")

	write(t, filepath.Join(e.StateDir, "pending"), "tag=v0.0.2\nprev=\ndeadline=1\n")
	write(t, filepath.Join(e.StateDir, "state"), "pending\n")

	f := &fake{verifyRC: []int{2}}
	var out strings.Builder
	Run(opts(e, f, &out))

	wantAbsent(t, e.QuarantineFile)
	if !strings.Contains(out.String(), "no last good release on disk") {
		t.Errorf("the run does not say why it kept the release:\n%s", out.String())
	}
	wantFile(t, filepath.Join(e.StateDir, "state"), "steady")
}

func TestAForcedReinstallIsNotSomethingToRollBackTo(t *testing.T) {
	e := newEnv(t)
	mkRelease(t, e, "v0.0.104")
	if err := os.MkdirAll(e.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, e.CurrentFile, "v0.0.104\n")
	write(t, filepath.Join(e.StateDir, "pending"), "tag=v0.0.104\nprev=v0.0.104\ndeadline=1\n")
	write(t, filepath.Join(e.StateDir, "state"), "pending\n")

	f := &fake{verifyRC: []int{2}}
	var out strings.Builder
	Run(opts(e, f, &out))

	wantAbsent(t, e.QuarantineFile)
	wantFile(t, e.CurrentFile, "v0.0.104")

	for _, c := range f.calls {
		if strings.HasPrefix(c, "activate v0.0.104") {
			t.Errorf("it reinstalled the release it was rolling back from: %q", f.calls)
		}
	}
}

func TestForceAbandonsAPendingWaitAndDeploysAgain(t *testing.T) {
	e := newEnv(t)
	mkRelease(t, e, "v0.0.104")
	if err := os.MkdirAll(e.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, e.CurrentFile, "v0.0.104\n")
	write(t, filepath.Join(e.StateDir, "pending"), "tag=v0.0.104\nprev=\ndeadline=9999999999\n")
	write(t, filepath.Join(e.StateDir, "state"), "pending\n")

	f := &fake{trackTag: "v0.0.104", verifyRC: []int{0, 0}}
	var out strings.Builder
	o := opts(e, f, &out)
	o.Force = true
	Run(o)

	if !strings.Contains(out.String(), "abandoning the wait") {
		t.Errorf("--force did not say what it was doing:\n%s", out.String())
	}
	var activated bool
	for _, c := range f.calls {
		if strings.HasPrefix(c, "activate v0.0.104") {
			activated = true
		}
	}
	if !activated {
		t.Errorf("--force verified instead of deploying again: %q", f.calls)
	}
}

func TestWithoutForceAPendingHostStillWaits(t *testing.T) {
	e := newEnv(t)
	mkRelease(t, e, "v0.0.104")
	if err := os.MkdirAll(e.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, e.CurrentFile, "v0.0.104\n")
	write(t, filepath.Join(e.StateDir, "pending"), "tag=v0.0.104\nprev=\ndeadline=9999999999\n")
	write(t, filepath.Join(e.StateDir, "state"), "pending\n")

	f := &fake{verifyRC: []int{1}}
	var out strings.Builder
	Run(opts(e, f, &out))
	if !strings.Contains(out.String(), "waiting on v0.0.104") {
		t.Errorf("a pending host did not wait:\n%s", out.String())
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "activate ") {
			t.Errorf("it deployed without being asked to: %q", f.calls)
		}
	}
}

func TestStallForwardIsAFlagAndTheEnvStillWorks(t *testing.T) {
	for _, c := range []struct {
		name string
		f    Flags
		env  string
	}{
		{"the flag", Flags{StallForward: true}, ""},
		{"the variable it replaced", Flags{}, "1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.env != "" {
				t.Setenv("NETWORK_STALL_FORWARD", c.env)
			}
			if got := c.f.env(); !got.StallForward {
				t.Errorf("stall-forward did not survive: %+v", got)
			}
		})
	}
}

func TestPolicyFlagOverridesTheConfiguredOne(t *testing.T) {
	e := track.Env{UpdatePolicy: "apply"}
	if got := policyOf(e, Flags{Policy: "off"}); got != "off" {
		t.Errorf("--policy off was ignored: %q", got)
	}
	if got := policyOf(e, Flags{}); got != "apply" {
		t.Errorf("no flag should leave the host's policy alone: %q", got)
	}

	t.Setenv("KNO_UPDATE_POLICY", "simulate")
	if got := policyOf(e, Flags{}.env()); got != "simulate" {
		t.Errorf("KNO_UPDATE_POLICY was dropped: %q", got)
	}
}

func TestTheFleetsVerdictsNameTheNetworkOrTheHost(t *testing.T) {
	for _, c := range []struct {
		name     string
		verdicts map[string]int
		want     []string
		absent   []string
	}{
		{
			name: "every host says the network has none",
			verdicts: map[string]int{
				"annares": verify.CodeNoConsensus, "mixy": verify.CodeNoConsensus,
				"reed": verify.CodeNoConsensus,
			},
			want: []string{"That is the NETWORK, not", "none can fix it"},
		},
		{
			name: "most say so, a couple are in",
			verdicts: map[string]int{
				"annares": verify.CodeNoConsensus, "mixy": verify.CodeNoConsensus,
				"reed": verify.CodeNoConsensus, "lilly": 0, "haywood": verify.CodeNoConsensus,
			},
			want: []string{"4 of 5 hosts report no consensus", "probably in the"},
		},
		{
			name: "consensus exists and one host is out of it",
			verdicts: map[string]int{
				"annares": 0, "mixy": 0, "reed": 0, "lilly": verify.CodeNoConsensus,
			},
			want: []string{"Consensus exists", "kno doctor --host lilly"},

			absent: []string{"That is the NETWORK"},
		},
		{
			name: "a host that could not ask is a local fault",
			verdicts: map[string]int{
				"annares": 0, "lilly": verify.CodeCannotAsk,
			},
			want: []string{"could not ask", "says nothing about the network"},
		},
		{
			name:     "every host in consensus says nothing at all",
			verdicts: map[string]int{"annares": 0, "mixy": 0},
			absent:   []string{"consensus across"},
		},
		{
			name:     "one host alone cannot compare",
			verdicts: map[string]int{"lilly": verify.CodeNoConsensus},
			absent:   []string{"consensus across"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out strings.Builder
			reportNetworkConsensus(track.Logger{Tool: "update", W: &out}, c.verdicts)
			got := out.String()
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in:\n%s", w, got)
				}
			}
			for _, a := range c.absent {
				if strings.Contains(got, a) {
					t.Errorf("should not say %q:\n%s", a, got)
				}
			}
		})
	}
}

func TestNewConfigsAtTheSameTagAreInstalledAndKeepTheOldRelease(t *testing.T) {
	e := newEnv(t)
	mkRelease(t, e, "v0.0.107")
	write(t, filepath.Join(e.ReleasesDir, "v0.0.107", "pkgs", "old.pkg"), "old\n")
	write(t, e.CurrentFile, "v0.0.107\n")
	write(t, filepath.Join(e.StateDir, "configs"), "a=+1\n")
	f := &fake{trackTag: "v0.0.107", configs: "a=+2"}
	var out strings.Builder
	if rc := Run(opts(e, f, &out)); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, out.String())
	}
	wantCalls(t, f.calls, []string{
		"track",
		`verify "" consensus-only=true`,
		"build v0.0.107",
		"activate v0.0.107 " + filepath.Join(e.ReleasesDir, "v0.0.107"),
	})
	wantFile(t, filepath.Join(e.StateDir, "configs"), "a=+2")
	wantFile(t, filepath.Join(e.ReleasesDir, "v0.0.107.last", "pkgs", "old.pkg"), "old")
	wantAbsent(t, filepath.Join(e.StateDir, "last-known-good"))
}

func TestConfigsThatFailVerifyRollBackFromTheCopyAndKeepTheTag(t *testing.T) {
	e := newEnv(t)
	mkRelease(t, e, "v0.0.107", "v0.0.107.last")
	write(t, e.CurrentFile, "v0.0.107\n")
	write(t, filepath.Join(e.StateDir, "configs"), "a=+2\n")
	write(t, filepath.Join(e.StateDir, "pending"),
		"tag=v0.0.107\nprev=v0.0.107\ndeadline=1\nconfigs=a=+2\nprevconfigs=a=+1\n")
	write(t, filepath.Join(e.StateDir, "state"), "pending\n")
	f := &fake{verifyRC: []int{2}}
	var out strings.Builder
	Run(opts(e, f, &out))
	wantCalls(t, f.calls, []string{
		fmt.Sprintf("verify %q consensus-only=false", filepath.Join(e.ReleasesDir, "v0.0.107")),
		"activate v0.0.107 " + filepath.Join(e.ReleasesDir, "v0.0.107.last"),
	})
	wantFile(t, filepath.Join(e.StateDir, "configs"), "a=+1")
	wantFile(t, filepath.Join(e.StateDir, "configs-failed"), "a=+2")
	wantFile(t, e.CurrentFile, "v0.0.107")
	wantAbsent(t, e.QuarantineFile)
}

func TestTheSameTagDecision(t *testing.T) {
	for _, c := range []struct {
		want, installed, failed string
		rc                      int
		says                    string
	}{
		{"a=+1 b=+1", "a=+1 b=+1", "", 3, "Nothing to do"},
		{"a=+1 b=+2", "a=+1 b=+1", "", 0, "new config packages for b"},
		{"a=+1 b=+2", "a=+1 b=+1", "a=+1 b=+2", 3, "did not pass verify"},
		{"a=+1", "", "", 0, "does not know its config packages"},
	} {
		rc, msg := sameTag("v0.0.107", c.want, c.installed, c.failed)
		if rc != c.rc || !strings.Contains(msg, c.says) {
			t.Errorf("sameTag(%q, %q, %q) = %d %q, want %d and %q", c.want, c.installed, c.failed, rc, msg, c.rc, c.says)
		}
	}
}

func TestARollbackAcrossTagsRestoresTheConfigsRecord(t *testing.T) {
	e := newEnv(t)
	mkRelease(t, e, "v0.0.105", "v0.0.107")
	write(t, e.CurrentFile, "v0.0.107\n")
	write(t, filepath.Join(e.StateDir, "configs"), "a=+2\n")
	write(t, filepath.Join(e.StateDir, "pending"),
		"tag=v0.0.107\nprev=v0.0.105\ndeadline=1\nconfigs=a=+2\nprevconfigs=a=+1\n")
	write(t, filepath.Join(e.StateDir, "state"), "pending\n")
	f := &fake{verifyRC: []int{2}}
	var out strings.Builder
	Run(opts(e, f, &out))
	wantFile(t, e.CurrentFile, "v0.0.105")
	wantFile(t, filepath.Join(e.StateDir, "configs"), "a=+1")
	wantFile(t, e.QuarantineFile, "v0.0.107")
}

func TestASecondUpdateWhileOneRunsDoesNothing(t *testing.T) {
	e := newEnv(t)
	if err := os.MkdirAll(e.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	unlock, ok, err := fsutil.TryLock(filepath.Join(e.StateDir, "update.lock"))
	if err != nil || !ok {
		t.Fatalf("lock: %v %v", ok, err)
	}
	defer unlock()
	f := &fake{trackTag: "v0.0.2"}
	var out strings.Builder
	if rc := Run(opts(e, f, &out)); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	if len(f.calls) != 0 {
		t.Errorf("a second update ran beside the first: %v", f.calls)
	}
	if !strings.Contains(out.String(), "another kno update holds") {
		t.Errorf("the second update did not say why it did nothing:\n%s", out.String())
	}
}

func TestPendingWaitsTwoEpochsBeforeItPromotes(t *testing.T) {
	for _, c := range []struct {
		epoch   string
		promote bool
	}{{"", false}, {"2m", true}} {
		t.Setenv("KATZENPOST_EPOCH_DURATION", c.epoch)
		e := newEnv(t)
		mkRelease(t, e, "v0.0.1", "v0.0.2")
		write(t, filepath.Join(e.StateDir, "state"), "pending\n")
		write(t, filepath.Join(e.StateDir, "pending"),
			fmt.Sprintf("tag=v0.0.2\nprev=v0.0.1\nactivated=%d\ndeadline=%d\n",
				testNow.Unix()-6*60, testNow.Unix()+3600))
		f := &fake{verifyRC: []int{0}}
		var out strings.Builder
		if rc := Run(opts(e, f, &out)); rc != 0 {
			t.Fatalf("rc = %d\n%s", rc, out.String())
		}
		if c.promote {
			wantFile(t, filepath.Join(e.StateDir, "state"), "steady")
			wantFile(t, filepath.Join(e.StateDir, "last-known-good"), "v0.0.2")
			continue
		}
		wantFile(t, filepath.Join(e.StateDir, "state"), "pending")
		if !strings.Contains(out.String(), "The first kno update from") {
			t.Errorf("a verify 6 minutes after the install promoted, or said nothing:\n%s", out.String())
		}
	}
}

func TestPromoteSkipsTheTwoEpochWait(t *testing.T) {
	t.Setenv("KATZENPOST_EPOCH_DURATION", "")
	e := newEnv(t)
	mkRelease(t, e, "v0.0.1", "v0.0.2")
	write(t, filepath.Join(e.StateDir, "state"), "pending\n")
	write(t, filepath.Join(e.StateDir, "pending"),
		fmt.Sprintf("tag=v0.0.2\nprev=v0.0.1\nactivated=%d\ndeadline=%d\n", testNow.Unix()-6*60, testNow.Unix()+3600))
	f := &fake{verifyRC: []int{0}}
	var out strings.Builder
	o := opts(e, f, &out)
	o.Promote = true
	if rc := Run(o); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, out.String())
	}
	wantFile(t, filepath.Join(e.StateDir, "state"), "steady")
	wantFile(t, filepath.Join(e.StateDir, "last-known-good"), "v0.0.2")
}

func TestAnInstallKeepsTheLastGoodReleaseThatVerifyPassed(t *testing.T) {
	e := newEnv(t)
	mkRelease(t, e, "v0.0.1", "v0.0.2")
	write(t, e.CurrentFile, "v0.0.2\n")
	write(t, filepath.Join(e.StateDir, "last-known-good"), "v0.0.1\n")
	f := &fake{trackTag: "v0.0.3", verifyRC: []int{0}}
	var out strings.Builder
	if rc := Run(opts(e, f, &out)); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, out.String())
	}
	wantFile(t, filepath.Join(e.StateDir, "last-known-good"), "v0.0.1")
	wantFile(t, e.CurrentFile, "v0.0.3")
}

func TestWithoutAnEarlierBuildAConfigRollbackIsRefused(t *testing.T) {
	e := newEnv(t)
	mkRelease(t, e, "v0.0.107", "v0.0.107.last")
	write(t, e.CurrentFile, "v0.0.107\n")
	write(t, filepath.Join(e.StateDir, "configs"), "a=+2\n")
	write(t, filepath.Join(e.StateDir, "pending"),
		"tag=v0.0.107\nprev=v0.0.107\ndeadline=1\nconfigs=a=+2\nprevconfigs=a=+1\n")
	write(t, filepath.Join(e.StateDir, "state"), "pending\n")
	f := &fake{verifyRC: []int{2}}
	var out strings.Builder
	o := opts(e, f, &out)
	o.NoEarlierBuild = true
	if rc := Run(o); rc != 1 {
		t.Errorf("rc = %d, want 1\n%s", rc, out.String())
	}
	wantCalls(t, f.calls, []string{
		fmt.Sprintf("verify %q consensus-only=false", filepath.Join(e.ReleasesDir, "v0.0.107")),
	})
	wantFile(t, filepath.Join(e.StateDir, "configs"), "a=+2")
	wantFile(t, filepath.Join(e.StateDir, "configs-failed"), "a=+2")
	wantFile(t, filepath.Join(e.StateDir, "state"), "steady")
	wantAbsent(t, filepath.Join(e.StateDir, "pending"))
	if !strings.Contains(out.String(), "stay installed") {
		t.Errorf("the refusal does not say that the new config packages stay installed:\n%s", out.String())
	}
}

func TestWithoutAnEarlierBuildNoCopyIsKept(t *testing.T) {
	e := newEnv(t)
	mkRelease(t, e, "v0.0.107")
	write(t, e.CurrentFile, "v0.0.107\n")
	write(t, filepath.Join(e.StateDir, "configs"), "a=+1\n")
	f := &fake{trackTag: "v0.0.107", configs: "a=+2"}
	var out strings.Builder
	o := opts(e, f, &out)
	o.NoEarlierBuild = true
	if rc := Run(o); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, out.String())
	}
	wantAbsent(t, filepath.Join(e.ReleasesDir, "v0.0.107.last"))
}

func TestAFirstInstallThatFailsVerifyWithNothingToRollBackToIsAFailure(t *testing.T) {
	e := newEnv(t)
	mkRelease(t, e, "v0.0.1")
	f := &fake{trackTag: "v0.0.1", verifyRC: []int{2}}
	var out strings.Builder
	if rc := Run(opts(e, f, &out)); rc != 0 {
		t.Fatalf("the first run: rc = %d\n%s", rc, out.String())
	}
	out.Reset()
	if rc := Run(opts(e, f, &out)); rc != 1 {
		t.Errorf("verify failed and nothing can be rolled back to: rc = %d, want 1\n%s", rc, out.String())
	}
	if !strings.Contains(out.String(), "kno cannot roll back") {
		t.Errorf("the run does not say that it cannot roll back:\n%s", out.String())
	}
	wantFile(t, filepath.Join(e.StateDir, "state"), "steady")
	wantFile(t, e.CurrentFile, "v0.0.1")
	if e.Quarantined("v0.0.1") {
		t.Error("kno quarantined the only release this host has")
	}
}
