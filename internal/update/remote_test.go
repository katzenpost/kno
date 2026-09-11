package update

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/push"
	"github.com/katzenpost/kno/internal/record"
	"github.com/katzenpost/kno/internal/testutil"
	"github.com/katzenpost/kno/internal/track"
)

func fakeSSH(t *testing.T, code int, say string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ssh")
	body := "#!/bin/sh\necho '" + say + "'\nexit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func runnerWith(w *bytes.Buffer, ssh string) *RemoteRunner {
	h := remoteHost{Label: "mixy", Node: "root@mixy", Services: []string{"mixy-pq-mixserver"}}
	return &RemoteRunner{
		Env: track.Env{}, Host: h, SSH: ssh, Out: w,
		Log: track.Logger{Tool: "update", W: w},
	}
}

func TestUnreachableHostIsNotEvidenceAgainstTheRelease(t *testing.T) {
	var b bytes.Buffer
	r := runnerWith(&b, fakeSSH(t, 255, "ssh: connect to host mixy port 22: No route to host"))
	if got := r.Verify("", false); got != VerifyUnavailable {
		t.Fatalf("Verify = %d, want VerifyUnavailable (%d); an unreachable host"+
			" would roll back a healthy release", got, VerifyUnavailable)
	}
	if !bytes.Contains(b.Bytes(), []byte("ssh cannot connect")) {
		t.Errorf("nothing said why:\n%s", b.String())
	}
}

func TestAHostWithNoKnoCannotVerify(t *testing.T) {
	var b bytes.Buffer
	r := runnerWith(&b, fakeSSH(t, 127, "kno: command not found"))
	if got := r.Verify("", false); got != VerifyUnavailable {
		t.Errorf("Verify = %d, want VerifyUnavailable (%d)", got, VerifyUnavailable)
	}
}

func TestTheRemoteVerdictPassesThrough(t *testing.T) {
	for _, code := range []int{0, 1, 2} {
		var b bytes.Buffer
		r := runnerWith(&b, fakeSSH(t, code, "verify says so"))
		if got := r.Verify("", false); got != code {
			t.Errorf("Verify = %d, want the remote's own %d", got, code)
		}
	}
}

func TestThePreflightIsQuietOnlyWhenItPasses(t *testing.T) {
	var ok bytes.Buffer
	runnerWith(&ok, fakeSSH(t, 0, "consensus present")).Verify("", true)
	if bytes.Contains(ok.Bytes(), []byte("consensus present")) {
		t.Errorf("a passing pre-flight printed the child's output:\n%s", ok.String())
	}

	var bad bytes.Buffer
	runnerWith(&bad, fakeSSH(t, 1, "no consensus for this node")).Verify("", true)
	if !bytes.Contains(bad.Bytes(), []byte("no consensus for this node")) {
		t.Errorf("a failing pre-flight hid its evidence:\n%s", bad.String())
	}
}

func TestTheReleaseIsBuiltOnceForEveryHost(t *testing.T) {
	s := &stage{env: track.Env{Repo: t.TempDir(), Network: "namenlos"}}
	first := s.ensure("v0.0.104")
	if first == nil {
		t.Fatal("a build with no resolvable service was expected to fail")
	}

	if got := s.ensure("v0.0.104"); got != first {
		t.Errorf("the second host rebuilt instead of reusing the staged release")
	}
}

func TestTheBuildCheckOfOneTagIsNotReusedForAnother(t *testing.T) {
	e := track.Env{Repo: testutil.RepoRoot(t), Network: "namenlos",
		ReleasesDir: filepath.Join(t.TempDir(), "releases")}
	var b bytes.Buffer
	s := &stage{env: e, hosts: []remoteHost{mixyHost()}, log: track.Logger{Tool: "update", W: &b}}
	if err := s.ensure("v0.0.104"); err == nil || !strings.Contains(err.Error(), "v0.0.104") {
		t.Fatalf("ensure v0.0.104 = %v, want no build of v0.0.104", err)
	}
	if err := s.ensure("v0.0.107"); err == nil || !strings.Contains(err.Error(), "v0.0.107") {
		t.Errorf("ensure v0.0.107 = %v: the answer for v0.0.104 was used for v0.0.107", err)
	}
}

func TestARemoteUpdateBuildsWhatEachBackendInstalls(t *testing.T) {
	repo := testutil.RepoRoot(t)
	for _, tc := range []struct {
		svc                  string
		skipFreeBSD, skipDeb bool
	}{
		{"respectmy-pq-authority", false, true},
		{"mixy-pq-mixserver", true, false},
	} {
		s := &stage{env: track.Env{Repo: repo, Network: "namenlos"},
			hosts: []remoteHost{{Label: "h", Services: []string{tc.svc}}}}
		o := s.options("v0.0.107")
		if o.SkipFreeBSD != tc.skipFreeBSD || o.SkipDeb != tc.skipDeb {
			t.Errorf("%s: SkipFreeBSD=%v SkipDeb=%v, want %v %v; push needs the packages of its backend",
				tc.svc, o.SkipFreeBSD, o.SkipDeb, tc.skipFreeBSD, tc.skipDeb)
		}
	}
}

func TestOneHostCanBeUpdatedOnItsOwn(t *testing.T) {
	all := []remoteHost{
		{Label: "annares"}, {Label: "haywood"}, {Label: "mixy"}, {Label: "reed"},
	}
	got, err := selectHosts(all, "mixy")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Label != "mixy" {
		t.Fatalf("got %+v, want just mixy", got)
	}

	if got, err = selectHosts(all, "mixy,reed"); err != nil || len(got) != 2 {
		t.Fatalf("got %+v (%v), want mixy and reed", got, err)
	}

	_, err = selectHosts(all, "mixxy")
	if err == nil {
		t.Fatal("a host that does not exist was accepted")
	}
	for _, want := range []string{"mixxy", "annares", "mixy"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
}

func TestForceReachesTheRunner(t *testing.T) {
	r := &RemoteRunner{Force: true}
	if !r.Force {
		t.Fatal("Force is not readable where Track looks for it")
	}
}

func TestPreflightRefusesTheRunAndNamesEveryHostItCannotServe(t *testing.T) {
	hosts := []remoteHost{{Label: "annares"}, {Label: "haywood"}, {Label: "mixy"}}
	var b bytes.Buffer
	l := track.Logger{Tool: "update", W: &b}

	ok := preflight(l, hosts, "v0.0.104", func(label string) error {
		if label == "annares" {
			return nil
		}
		return fmt.Errorf("no kno package for amd64")
	})
	if ok {
		t.Fatal("a release that cannot serve two of three hosts must not be deployed")
	}
	out := b.String()
	for _, want := range []string{"haywood", "mixy", "no kno package for amd64"} {
		if !strings.Contains(out, want) {
			t.Errorf("report does not mention %q:\n%s", want, out)
		}
	}

	if strings.Count(out, "no kno package for amd64") < 2 {
		t.Errorf("only one host's failure was reported:\n%s", out)
	}
	if !strings.Contains(out, "no host was contacted") {
		t.Errorf("report does not say that nothing happened:\n%s", out)
	}

	if !strings.Contains(out, "--host annares") {
		t.Errorf("report does not name the hosts that would be fine:\n%s", out)
	}
}

func TestPreflightIsSilentWhenEveryHostCouldBeServed(t *testing.T) {
	var b bytes.Buffer
	l := track.Logger{Tool: "update", W: &b}
	ok := preflight(l, []remoteHost{{Label: "annares"}, {Label: "mixy"}},
		"v0.0.104", func(string) error { return nil })
	if !ok {
		t.Fatal("a release that serves every host must be deployed")
	}
	if b.String() != "" {
		t.Errorf("a passing preflight said something: %q", b.String())
	}
}

func TestTheUpdateVerifyAsksWhetherTheNodeIsInTheConsensus(t *testing.T) {
	dir := t.TempDir()
	echo := filepath.Join(dir, "ssh")
	if err := os.WriteFile(echo, []byte("#!/bin/sh\necho \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ env, want string }{{"", "VERIFY_CHECK_PARTICIPATION=1 kno verify"},
		{"0", "VERIFY_CHECK_PARTICIPATION=0 kno verify"}, {"1; rm -rf /", "VERIFY_CHECK_PARTICIPATION=1 kno verify"}} {
		t.Setenv("VERIFY_CHECK_PARTICIPATION", c.env)
		var b bytes.Buffer
		r := runnerWith(&b, echo)
		if got := r.Verify("", false); got != 0 {
			t.Fatalf("Verify = %d\n%s", got, b.String())
		}
		if !strings.Contains(b.String(), c.want) {
			t.Errorf("env %q: the remote verify ran without %q:\n%s", c.env, c.want, b.String())
		}
	}
}

func TestANewerKnoAloneDoesNotReinstallAHost(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	e := newEnv(t)
	e.Repo = filepath.Join(e.Home, "network-organizer")
	e.KatzenpostRepo = filepath.Join(e.Home, "katzenpost")
	initRepo(t, e.Repo, "v0.0.2")
	initRepo(t, e.KatzenpostRepo, "v0.0.2")
	write(t, filepath.Join(e.Repo, track.VersionsFile), "v0.0.2\n")
	write(t, e.CurrentFile, "v0.0.2\n")
	var b bytes.Buffer
	r := &RemoteRunner{
		Env: e, Host: mixyHost(), Out: &b,
		Log:    track.Logger{Tool: "update", W: &b},
		Before: &record.Sample{Packages: map[string]string{"kno": "0.0.2-1+20200101000000.gold"}},
	}
	write(t, configsFile(e.StateDir), r.Configs()+"\n")
	if _, rc := r.Track(); rc != 3 {
		t.Fatalf("Track rc = %d, want 3: a host at the tag with its configs was reinstalled for an older kno\n%s", rc, b.String())
	}
	write(t, configsFile(e.StateDir), "mixy-pq-mixserver=+19990101000000.gold\n")
	if _, rc := r.Track(); rc != 0 {
		t.Fatalf("Track rc = %d, want 0: a config change at the same tag was not installed\n%s", rc, b.String())
	}
}

func TestTheUpdateSaysWhatChangedOnAHost(t *testing.T) {
	before := map[string]string{"katzenpost": "0.0.104+ubuntu24.04", "kno": "0.0.104-262",
		"namenlos-config-reed-pq-mixserver": "0.0.104-259"}
	if got := packageList(before); got != "katzenpost 0.0.104+ubuntu24.04, kno 0.0.104-262, namenlos-config-reed-pq-mixserver 0.0.104-259" {
		t.Errorf("packageList = %q", got)
	}
	after := map[string]string{"katzenpost": "0.0.107", "kno": "0.0.104-262",
		"namenlos-config-reed-pq-mixserver": "0.0.107-1+20260701154849.gdf160a9", "kno-host-config-reed": "0.0.107-1"}
	want := "katzenpost 0.0.107 (was 0.0.104+ubuntu24.04), kno-host-config-reed 0.0.107-1 (was none)," +
		" namenlos-config-reed-pq-mixserver 0.0.107-1+20260701154849.gdf160a9 (was 0.0.104-259)"
	if got := packageChanges(before, after); got != want {
		t.Errorf("packageChanges\n got: %s\nwant: %s", got, want)
	}
	if got := packageChanges(before, before); got != "" {
		t.Errorf("an unchanged host printed changes: %q", got)
	}
}

func TestASteadyHostWithAnotherKnoGetsOnlyKno(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	e := newEnv(t)
	e.Repo = filepath.Join(e.Home, "network-organizer")
	initRepo(t, e.Repo, "v0.0.2")
	write(t, filepath.Join(e.Repo, "thirdparty", "pins.env"), "KATZENPOST_REF=v0.0.2\n")
	git(t, e.Repo, "add", "-A")
	git(t, e.Repo, "commit", "-q", "-m", "pins")
	want := push.KnoVersionFor(e.Repo, "v0.0.2")
	if want == "" {
		t.Fatal("no kno version for the fixture repository")
	}
	write(t, e.CurrentFile, "v0.0.2\n")
	write(t, filepath.Join(e.StateDir, "state"), "steady\n")
	old := &record.Sample{Packages: map[string]string{"kno": "0.0.2-1+19990101000000.gold"}}
	if !knoDiffers(e, "v0.0.2", old) {
		t.Errorf("a steady host with kno %s and a repo that builds %s gets no new kno", old.Packages["kno"], want)
	}
	newer := &record.Sample{Packages: map[string]string{"kno": "0.0.2-1+29990101000000.gnew"}}
	if !knoDiffers(e, "v0.0.2", newer) {
		t.Errorf("a steady host with kno %s keeps it, and the repo builds %s", newer.Packages["kno"], want)
	}
	same := &record.Sample{Packages: map[string]string{"kno": want}}
	if knoDiffers(e, "v0.0.2", same) {
		t.Error("a host that runs the kno this repo builds is sent kno again")
	}
	write(t, filepath.Join(e.StateDir, "state"), "pending\n")
	if knoDiffers(e, "v0.0.2", old) {
		t.Error("a pending host is sent kno during its verify window")
	}
}

func TestAPendingHostsRecordStaysOutOfTheRepository(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "networks", "namenlos"), 0o755); err != nil {
		t.Fatal(err)
	}
	e := track.Env{Repo: repo, Network: "namenlos"}
	he := newEnv(t)
	rec := record.Record{Host: "haywood", Deploying: &record.Deploying{Tag: "v0.0.107"}}

	write(t, filepath.Join(he.StateDir, "state"), "pending\n")
	if _, err := persistRecord(e, he, rec, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(record.Path(repo, "namenlos", "haywood")); err == nil {
		t.Error("a pending host's record was written into the repository")
	}
	got, err := record.LoadPending(he.StateDir, repo, "namenlos", "haywood")
	if err != nil || got.Deploying == nil || got.Deploying.Tag != "v0.0.107" {
		t.Errorf("the pending record is not read back from the state: %+v %v", got, err)
	}

	write(t, filepath.Join(he.StateDir, "state"), "steady\n")
	if _, err := persistRecord(e, he, rec, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(record.Path(repo, "namenlos", "haywood")); err != nil {
		t.Error("a settled host's record is not in the repository")
	}
	if _, err := os.Stat(record.PendingPath(he.StateDir)); err == nil {
		t.Error("the pending copy survived the landing")
	}
}

func loggingSSH(t *testing.T, say string) (ssh, log string) {
	t.Helper()
	dir := t.TempDir()
	ssh, log = filepath.Join(dir, "ssh"), filepath.Join(dir, "log")
	body := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\necho '" + say + "'\n"
	if err := os.WriteFile(ssh, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return ssh, log
}

func TestThePendingVerifyCountsOnlyEvidenceSinceTheInstall(t *testing.T) {
	ssh, log := loggingSSH(t, "verified")
	var b bytes.Buffer
	r := runnerWith(&b, ssh)
	r.SetSince("@1792000000")
	r.Verify("", false)
	r.Verify("", true)
	r.SetSince("@1792000000; reboot")
	got, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(got)), "\n")
	if len(lines) != 2 {
		t.Fatalf("ssh calls = %q", lines)
	}
	if !strings.Contains(lines[0], "VERIFY_SINCE=@1792000000 VERIFY_CHECK_PARTICIPATION=1 kno verify") {
		t.Errorf("the pending verify does not pass the install time: %q", lines[0])
	}
	if strings.Contains(lines[1], "VERIFY_SINCE") {
		t.Errorf("the consensus pre-flight got the install time: %q", lines[1])
	}
	if r.since != "@1792000000" {
		t.Errorf("a since value that is not @<seconds> was taken: %q", r.since)
	}
}

func TestASampleAsksForTheUnitsOfTheReleaseTheHostRuns(t *testing.T) {
	for cur, want := range map[string]string{
		"v0.0.104": "pq-katzenpost-mixserver",
		"v0.0.107": "kpserver@mixy-pq-mixserver",
	} {
		ssh, log := loggingSSH(t, "#units")
		dir := t.TempDir()
		e := hostState(t, dir, map[string]string{"current": cur + "\n"})
		e.Repo, e.Network = testutil.RepoRoot(t), "namenlos"
		var b bytes.Buffer
		r := runnerWith(&b, ssh)
		r.Env = e
		r.sample("")
		got, _ := os.ReadFile(log)
		if !strings.Contains(string(got), "systemctl is-active "+want) &&
			!strings.Contains(string(got), want) {
			t.Errorf("a host on %s was asked about:\n%s\nwant unit %s", cur, got, want)
		}
		other := "kpserver@mixy-pq-mixserver"
		if cur != "v0.0.104" {
			other = "pq-katzenpost-mixserver "
		}
		if strings.Contains(string(got), other) {
			t.Errorf("a host on %s was asked about %s:\n%s", cur, other, got)
		}
	}
}

func TestTheRecordOfAHeldOrUnconfirmedHostLandsAtTheEnd(t *testing.T) {
	for _, c := range []struct {
		name    string
		pending string
		final   bool
		lands   bool
	}{
		{"in the window", "tag=v0.0.107\ndeadline=9999999999\n", false, false},
		{"held", "tag=v0.0.107\ndeadline=1\nheld=1\n", false, true},
		{"the run ended unconfirmed", "tag=v0.0.107\ndeadline=1\n", true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			repo := t.TempDir()
			if err := os.MkdirAll(filepath.Join(repo, "networks", "namenlos"), 0o755); err != nil {
				t.Fatal(err)
			}
			e := track.Env{Repo: repo, Network: "namenlos"}
			he := newEnv(t)
			write(t, filepath.Join(he.StateDir, "state"), "pending\n")
			write(t, filepath.Join(he.StateDir, "pending"), c.pending)
			rec := record.Record{Host: "annares", Deploying: &record.Deploying{Tag: "v0.0.107",
				Held: strings.Contains(c.pending, "held=1")}}
			if _, err := persistRecord(e, he, rec, c.final); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(record.Path(repo, "namenlos", "annares"))
			if (err == nil) != c.lands {
				t.Errorf("record in the repository = %v, want %v", err == nil, c.lands)
			}
		})
	}
	held := record.Record{Deploying: &record.Deploying{Tag: "v0.0.107", From: "v0.0.104", Held: true}}
	if s := held.Summary(); !strings.Contains(s, "held") {
		t.Errorf("the summary of a held host does not say held: %q", s)
	}
}

func preflightEnv(t *testing.T) track.Env {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	for _, v := range []string{"KNO_HOME", "KNO_ARTIFACTS", "XDG_DATA_HOME"} {
		t.Setenv(v, "")
	}
	t.Setenv("SSH", fakeSSH(t, 0, ""))
	base := t.TempDir()
	return track.Env{Repo: t.TempDir(), Network: "namenlos", StateDir: filepath.Join(base, "state"),
		ReleasesDir: filepath.Join(base, "releases")}
}

func TestTheStatePreflightReadsTheStateOfThisRun(t *testing.T) {
	e := preflightEnv(t)
	h := remoteHost{Label: "mixy", Node: "root@mixy"}
	he := hostEnv(e, h)
	write(t, he.CurrentFile, "v0.0.104\n")
	write(t, filepath.Join(he.StateDir, "last-known-good"), "v0.0.104\n")
	var b bytes.Buffer
	if stateReady(track.Logger{Tool: "update", W: &b}, &b, e, "v0.0.107", []remoteHost{h}) {
		t.Errorf("a last good release that is not on disk did not stop the run:\n%s", b.String())
	}
}

func TestAHostWithTheTargetQuarantinedDoesNotStopTheRun(t *testing.T) {
	e := preflightEnv(t)
	quarantined := remoteHost{Label: "a", Node: "root@a"}
	other := remoteHost{Label: "b", Node: "root@b"}
	write(t, hostEnv(e, quarantined).QuarantineFile, "v0.0.107\n")
	var b bytes.Buffer
	l := track.Logger{Tool: "update", W: &b}
	if !stateReady(l, &b, e, "v0.0.107", []remoteHost{quarantined, other}) {
		t.Errorf("a host that kno skips stopped the run for every host:\n%s", b.String())
	}
	write(t, hostEnv(e, other).CurrentFile, "v0.0.104\n")
	write(t, filepath.Join(hostEnv(e, other).StateDir, "last-known-good"), "v0.0.104\n")
	if stateReady(l, &b, e, "v0.0.107", []remoteHost{quarantined, other}) {
		t.Errorf("the other host was not checked:\n%s", b.String())
	}
}

func TestAVerifyWithNoAnswerIsNotRecordedAsAPass(t *testing.T) {
	t.Setenv("KNO_SSH_TIMEOUT", "100ms")
	for name, ssh := range map[string]string{
		"no answer":  fakeSSHBody(t, "#!/bin/sh\nexec sleep 5\n"),
		"no connect": fakeSSH(t, 255, "ssh: connect to host mixy port 22: No route to host"),
	} {
		var b bytes.Buffer
		r := runnerWith(&b, ssh)
		if got := r.Verify("", false); got != VerifyUnavailable {
			t.Errorf("%s: Verify = %d, want %d", name, got, VerifyUnavailable)
		}
		if v := verdictOf(r.verified); v == record.VerifiedOK || v == "not yet" {
			t.Errorf("%s: the record says verified %q", name, v)
		}
	}
}

func fakeSSHBody(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ssh")
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}
