package status

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/push"
	"github.com/katzenpost/kno/internal/record"
	"github.com/katzenpost/kno/internal/testutil"
	"github.com/katzenpost/kno/internal/track"
)

func taggedRepo(t *testing.T, tag string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "core.hooksPath=/dev/null", "-c", "user.name=kno-test", "-c", "user.email=kno@test", "commit", "--allow-empty", "-q", "-m", "root"},
		{"tag", tag},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	vp := filepath.Join(dir, track.VersionsFile)
	if err := os.MkdirAll(filepath.Dir(vp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vp, []byte(tag+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func envWithHosts(t *testing.T, art string) track.Env {
	t.Helper()
	t.Setenv("KNO_ARTIFACTS", art)
	repo := taggedRepo(t, "v0.0.104")
	state := t.TempDir()
	var e track.Env
	e.Repo, e.KatzenpostRepo = repo, repo
	e.StateDir = state
	e.CurrentFile = filepath.Join(state, "current")
	e.QuarantineFile = filepath.Join(state, "quarantine")
	e.Config = config.Config{
		Network: "namenlos",
		Nodes:   []string{"mixy", "reed"},
		Hosts: map[string]config.Host{
			"mixy": {SSH: "root@mixy", Services: []string{"mixy-pq-mixserver"}},
			"reed": {SSH: "root@reed", Services: []string{"reed-pq-mixserver"}},
		},
	}
	return e
}

func hostsReport(t *testing.T, e track.Env) string {
	t.Helper()
	var b bytes.Buffer
	reportHosts(&b, e)
	return b.String()
}

func TestReportHostsNeverDeployed(t *testing.T) {
	art := t.TempDir()
	out := hostsReport(t, envWithHosts(t, art))
	for _, want := range []string{"-- hosts --", "mixy", "reed", "root@mixy"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "UPDATE"); n != 2 {
		t.Errorf("want both hosts UPDATE, got %d:\n%s", n, out)
	}
	if !strings.Contains(out, "no record that this") {
		t.Errorf("want 'no record that this host installed on it':\n%s", out)
	}
}

func TestReportHostsMixedDelivery(t *testing.T) {
	art := t.TempDir()
	e := envWithHosts(t, art)
	if err := knopath.RecordPush("v0.0.104", "mixy", "ok"); err != nil {
		t.Fatal(err)
	}
	out := hostsReport(t, e)
	if strings.Count(out, "UPDATE") != 2 {

		t.Logf("report:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(knopath.PushReceipts("v0.0.104"), "mixy.txt")); err != nil {
		t.Fatalf("receipt not written: %v", err)
	}
	if got := pushedTags("mixy"); !got["v0.0.104"] {
		t.Errorf("pushedTags(mixy) = %v, want v0.0.104", got)
	}
	if got := pushedTags("reed"); len(got) != 0 {
		t.Errorf("pushedTags(reed) = %v, want none", got)
	}
}

func TestNewestTagIsVersionOrderNotStringOrder(t *testing.T) {
	if got := newest(map[string]bool{"v0.0.104": true, "v0.0.99": true}); got != "v0.0.104" {
		t.Errorf("newest = %q, want v0.0.104", got)
	}
	if got := newest(map[string]bool{"v0.0.104": true, "v0.0.99": true, "v0.0.105": true}); got != "v0.0.105" {
		t.Errorf("newest = %q, want v0.0.105", got)
	}
	if got := newest(map[string]bool{"v0.0.105": true, "v0.0.105-1": true}); got != "v0.0.105-1" {
		t.Errorf("newest = %q, want the revised tag v0.0.105-1", got)
	}
}

func TestServicesSectionOnAPushOnlyMachine(t *testing.T) {

	t.Setenv("KNO_STATUS_SSH", "0")
	e := envWithHosts(t, t.TempDir())
	e.Managed = []track.ManagedService{
		{Host: "mixy", SSH: "root@mixy", Service: "mixy-pq-mixserver"},
		{Host: "reed", SSH: "root@reed", Service: "reed-pq-mixserver"},
	}
	var b bytes.Buffer
	writeServices(&b, &bytes.Buffer{}, e, false, true)
	out := b.String()
	for _, want := range []string{
		"mixy-pq-mixserver", "root@mixy", "reed-pq-mixserver",
		"-- services -- (asked the hosts at ",

		"kpserver@mixy-pq-mixserver",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "managed") {
		t.Errorf("this tool organizes; it does not manage:\n%s", out)
	}
}

func TestReportHostsSilentWithoutHosts(t *testing.T) {
	t.Setenv("KNO_ARTIFACTS", t.TempDir())
	if out := hostsReport(t, track.Env{}); out != "" {
		t.Errorf("want no output, got:\n%s", out)
	}
}

func TestVersionsOnAnOrganizingMachineAreItsHosts(t *testing.T) {
	state := t.TempDir()
	e := track.Env{StateDir: state, Config: config.Config{Hosts: map[string]config.Host{
		"haywood": {SSH: "root@haywood"},
		"lilly":   {SSH: "root@lilly"},
	}}}
	for _, l := range []string{"haywood", "lilly"} {
		d := knopath.HostState(state, l)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, body := range map[string]string{
			"current": "v0.0.104\n", "last-known-good": "v0.0.104\n",
		} {
			if err := os.WriteFile(filepath.Join(d, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	v := versionsOf(e)
	if v.current != "v0.0.104" {
		t.Errorf("current = %q, want the version every host is on", v.current)
	}
	if v.lastGood != "v0.0.104" {
		t.Errorf("last-good = %q", v.lastGood)
	}
	if v.behind != "" {
		t.Errorf("every host is deployed, but it says %q", v.behind)
	}
	if !v.atTarget("v0.0.104") {
		t.Error("it does not consider itself up to date")
	}
}

func TestHostsOnDifferentVersionsAreAllNamed(t *testing.T) {
	state := t.TempDir()
	e := track.Env{StateDir: state, Config: config.Config{Hosts: map[string]config.Host{
		"mixy": {SSH: "root@mixy"}, "reed": {SSH: "root@reed"}, "annares": {SSH: "root@annares"},
	}}}
	for l, ver := range map[string]string{"mixy": "v0.0.104", "reed": "v0.0.102"} {
		d := knopath.HostState(state, l)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "current"), []byte(ver+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	v := versionsOf(e)
	for _, want := range []string{"v0.0.102 (reed)", "v0.0.104 (mixy)"} {
		if !strings.Contains(v.current, want) {
			t.Errorf("current %q does not name %s", v.current, want)
		}
	}

	if !strings.Contains(v.behind, "annares") {
		t.Errorf("a host that was never deployed is not reported: %q", v.behind)
	}
	if v.atTarget("v0.0.104") {
		t.Error("it claims to be up to date with a host on an older release")
	}
}

func TestANodeReportsItsOwnVersions(t *testing.T) {
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "current"), []byte("v0.0.104\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := track.Env{
		StateDir:    state,
		CurrentFile: filepath.Join(state, "current"),
		Services:    []string{"lilly-pq-servicenode"},
		Config:      config.Config{Hosts: map[string]config.Host{"lilly": {}}},
	}
	if got := versionsOf(e); got.current != "v0.0.104" || got.hosts != 0 {
		t.Errorf("versionsOf = %+v, want its own v0.0.104", got)
	}
}

func TestStatusReadsTheRecordUnlessAskedToGoLive(t *testing.T) {
	repo := t.TempDir()
	e := envWithHosts(t, t.TempDir())
	e.Repo, e.Network = repo, "namenlos"
	e.Managed = []track.ManagedService{
		{Host: "mixy", SSH: "root@mixy", Service: "mixy-pq-mixserver"},
	}
	rec := record.Record{
		Host: "mixy", Services: []string{"mixy-pq-mixserver"},
		Deployed: &record.Sample{Units: map[string]string{"pq-katzenpost-mixserver": "active"}},
	}
	if _, err := record.Write(repo, "namenlos", rec); err != nil {
		t.Fatal(err)
	}

	t.Setenv("KNO_STATUS_SSH", "")
	t.Setenv("SSH", "/nonexistent/ssh-must-not-be-run")

	var b bytes.Buffer
	writeServices(&b, &bytes.Buffer{}, e, false, false)
	out := b.String()
	if !strings.Contains(out, "the state that kno update saw last") {
		t.Errorf("the default does not say the states are a record:\n%s", out)
	}
	if !strings.Contains(out, "active") {
		t.Errorf("the recorded state was not reported:\n%s", out)
	}
	if !strings.Contains(out, "-l asks the hosts") {
		t.Errorf("it does not say how to ask the hosts:\n%s", out)
	}
}

func TestAPendingHostShowsWhatWasInstalled(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "networks", "namenlos"), 0o755); err != nil {
		t.Fatal(err)
	}
	e := track.Env{Repo: repo, Network: "namenlos"}
	rec := record.Record{Host: "reed",
		Observed:  &record.Sample{Packages: map[string]string{"katzenpost": "0.0.104+ubuntu24.04"}},
		Deploying: &record.Deploying{Tag: "v0.0.107", Packages: map[string]string{"katzenpost": "0.0.107"}},
	}
	if _, err := record.Write(repo, "namenlos", rec); err != nil {
		t.Fatal(err)
	}
	if got := installedVersions(e, "reed"); !strings.Contains(got, "katzenpost 0.0.107") ||
		!strings.Contains(got, "installed, in the verify window") {
		t.Errorf("a pending host shows %q, not what kno installed", got)
	}
	rec.Deploying.Packages = nil
	if _, err := record.Write(repo, "namenlos", rec); err != nil {
		t.Fatal(err)
	}
	if got := installedVersions(e, "reed"); !strings.Contains(got, "0.0.104+ubuntu24.04") ||
		!strings.Contains(got, "seen before the last install") {
		t.Errorf("a record with no installed list shows %q", got)
	}
}

func TestEachHostGetsOneMarkForItsState(t *testing.T) {
	dir := t.TempDir()
	e := track.Env{StateDir: dir}
	put := func(label string, files map[string]string) {
		d := knopath.HostState(dir, label)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		for n, b := range files {
			if err := os.WriteFile(filepath.Join(d, n), []byte(b), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	put("v", map[string]string{"state": "pending\n", "current": "v0.0.107\n",
		"pending": "tag=v0.0.107\nactivated=1792000000\npromote=1792002400\ndeadline=1792005400\nverdict=1\nchecked=1792000600\n"})
	put("h", map[string]string{"state": "pending\n", "current": "v0.0.107\n",
		"pending": "tag=v0.0.107\ndeadline=1792005400\nheld=1\nverdict=4\n"})
	put("f", map[string]string{"state": "steady\n", "current": "v0.0.104\n", "quarantine": "v0.0.107\n"})
	put("u", map[string]string{"state": "steady\n", "current": "v0.0.104\n"})
	put("o", map[string]string{"state": "steady\n", "current": "v0.0.107\n", "last-known-good": "v0.0.107\n"})
	put("n", map[string]string{"state": "steady\n", "current": "v0.0.107\n", "last-known-good": "v0.0.104\n"})
	put("z", map[string]string{"state": "steady\n", "current": "v0.0.107\n"})
	for label, want := range map[string][]string{
		"n": {"FAILED", "did not promote", "v0.0.104"},
		"z": {"FAILED", "did not promote", "none"},
		"v": {"VERIFY", "verifying v0.0.107", "last verify", "not yet", "promotion from", "window closes"},
		"h": {"HELD", "not promoted", "cannot ask", "kno update checks it again"},
		"f": {"FAILED", "quarantined", "kno unquarantine --host f v0.0.107"},
		"u": {"UPDATE", "has v0.0.104; the target is v0.0.107"},
		"o": {"ok", "has v0.0.107"},
	} {
		mark, detail := hostLine(e, label, "v0.0.107", nil)
		got := mark + " " + detail
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q does not say %q", label, got, w)
			}
		}
	}
}

func TestAHostWithAnOlderKnoHasAnUpdate(t *testing.T) {
	e := track.Env{StateDir: t.TempDir(), Repo: testutil.RepoRoot(t), Network: "namenlos"}
	d := knopath.HostState(e.StateDir, "mixy")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	for n, b := range map[string]string{"state": "steady\n", "current": "v0.0.107\n",
		"last-known-good": "v0.0.107\n"} {
		if err := os.WriteFile(filepath.Join(d, n), []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rec := record.Record{Host: "mixy", Deployed: &record.Sample{Packages: map[string]string{
		"katzenpost": "0.0.107", "kno": "0.0.107-1+20260101000000.g0000000"}}}
	if _, err := record.WriteFile(record.PendingPath(d), rec); err != nil {
		t.Fatal(err)
	}
	mark, detail := hostLine(e, "mixy", "v0.0.107", nil)
	if want := push.KnoVersionFor(e.Repo, "v0.0.107"); want == "" {
		t.Skip("this checkout builds no kno version for v0.0.107")
	}
	if mark != "UPDATE" || !strings.Contains(detail, "kno update installs kno") {
		t.Errorf("mark = %s %q", mark, detail)
	}
}

func TestConfigPackagesThatFailedVerifyAreNotAnUpdate(t *testing.T) {
	e := track.Env{StateDir: t.TempDir(), Repo: testutil.RepoRoot(t), Network: "namenlos",
		Config: config.Config{Hosts: map[string]config.Host{
			"mixy": {SSH: "root@mixy", Services: []string{"mixy-pq-mixserver"}},
		}}}
	want := "mixy-pq-mixserver=" + deb.ConfigStamp(e.Repo, e.Network, "mixy-pq-mixserver") + "\n"
	d := knopath.HostState(e.StateDir, "mixy")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, installed := range []string{"mixy-pq-mixserver=+19990101000000.gold\n", want} {
		for n, b := range map[string]string{"state": "steady\n", "current": "v0.0.107\n",
			"last-known-good": "v0.0.107\n", "configs": installed, "configs-failed": want} {
			if err := os.WriteFile(filepath.Join(d, n), []byte(b), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if got := configGap(e, "mixy"); len(got) != 0 {
			t.Errorf("configs %q: config packages that kno update does not install again are a gap: %v",
				installed, got)
		}
		mark, detail := hostLine(e, "mixy", "v0.0.107", nil)
		if mark != "FAILED" || !strings.Contains(detail, "did not pass verify") {
			t.Errorf("configs %q: mark = %s %q", installed, mark, detail)
		}
	}
}
