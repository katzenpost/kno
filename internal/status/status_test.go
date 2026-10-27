package status

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/service"
	"github.com/katzenpost/kno/internal/track"
	"github.com/katzenpost/kno/internal/verify"
)

func needTools(t *testing.T, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, err := exec.LookPath(n); err != nil {
			t.Skipf("%s not available", n)
		}
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "core.hooksPath=/dev/null", "-c", "user.name=kno-test", "-c", "user.email=kno@test"}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func initRepo(t *testing.T, dir string, tags ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q")
	git(t, dir, "commit", "--allow-empty", "-q", "-m", "root")
	for _, tag := range tags {
		git(t, dir, "tag", tag)
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

var fixtureServices = []string{
	"alpha-pq-authority",
	"alpha-pq-mixserver",
	"alpha-pq-storagenode-0",
	"courier",
	"ghost-pq-mixserver",
}

const knoTOML = `network = "testnet"
nodes = ["alpha"]

[update]
policy = "simulate"
require_signed_tag = false

[push]
mode = "full"

[hosts."testhost"]
scheduler = "none"
services = [%s]
`

const twoHostTOML = `network = "testnet"
nodes = ["alpha"]

[update]
policy = "simulate"
require_signed_tag = false

[push]
mode = "full"

[hosts."remote-a"]
ssh = "root@remote-a"
scheduler = "none"
services = ["alpha-pq-authority", "alpha-pq-mixserver"]

[hosts."remote-b"]
ssh = "root@remote-b"
scheduler = "none"
services = ["alpha-pq-storagenode-0"]
`

type fixture struct {
	home, repo, kp, state, toml string
	openPort, closedPort        int
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	home := t.TempDir()
	f := fixture{
		home:  home,
		repo:  filepath.Join(home, "network-organizer"),
		kp:    filepath.Join(home, "katzenpost"),
		state: filepath.Join(home, "state"),
		toml:  filepath.Join(home, "kno.toml"),
	}
	quoted := make([]string, len(fixtureServices))
	for i, s := range fixtureServices {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	write(t, f.toml, fmt.Sprintf(knoTOML, strings.Join(quoted, ", ")))

	initRepo(t, f.repo)
	write(t, filepath.Join(f.repo, "thirdparty", "katzenpost-versions"), "v0.0.1\nv0.0.2\nv0.0.3\n")
	initRepo(t, f.kp, "v0.0.1", "v0.0.2")
	write(t, filepath.Join(f.state, "current"), "v0.0.1\n")
	write(t, filepath.Join(f.state, "last-known-good"), "v0.0.1\n")
	write(t, filepath.Join(f.state, "quarantine"), "v0.0.3\n")

	f.openPort, f.closedPort = openPort(t), closedPort(t)
	gc := knopath.ConfigsDir(f.repo, "testnet")
	write(t, filepath.Join(gc, "alpha-pq-authority.toml"),
		fmt.Sprintf("[Server]\n  Addresses = [\"tcp://127.0.0.1:%d\"]\n", f.openPort))
	write(t, filepath.Join(gc, "alpha-pq-mixserver.toml"),
		fmt.Sprintf("[Server]\n  Addresses = [\"tcp://127.0.0.1:%d\"]\n", f.closedPort))
	write(t, filepath.Join(gc, "alpha-pq-storagenode-0.toml"), "[Server]\n")
	write(t, filepath.Join(gc, "courier.toml"), "[Courier]\n")

	for k, v := range map[string]string{
		"HOME":              f.home,
		"KNO_HOME":          f.home,
		"KNO_REPO":          f.repo,
		"KATZENPOST_REPO":   f.kp,
		"UPDATER_STATE_DIR": f.state,
		"KNO_CONFIG":        f.toml,
		"KNO_HOST":          "testhost",
		"KNO_GIT_FETCH":     "0",
		"KNO_CLONE_KP":      "0",
		"KNO_CLONE":         "0",
		"GIT_CONFIG_GLOBAL": os.DevNull,
		"GIT_CONFIG_SYSTEM": os.DevNull,

		"SERVICES":          "",
		"SERVICES_DIR":      "",
		"KNO_NETWORK":       "",
		"VERIFY_TAG":        "",
		"KNO_UPDATE_POLICY": "",
		"KNO_PUSH_MODE":     "",
		"QUARANTINE_FILE":   "",
		"CURRENT_FILE":      "",
		"KATZENPOST_URL":    "",
	} {
		t.Setenv(k, v)
	}
	return f
}

func openPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().(*net.TCPAddr).Port
}

func closedPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return p
}

func runGo(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var o, e bytes.Buffer
	code = Run(&o, &e, args)
	return o.String(), e.String(), code
}

func TestStatusShowsTheHost(t *testing.T) {
	needTools(t, "git", "systemctl")
	newFixture(t)

	goOut, goErr, goRC := runGo(t)
	if goRC != 0 {
		t.Errorf("exit status %d, want 0\n%s", goRC, goErr)
	}

	for _, want := range []string{
		"  current:    v0.0.1\n",
		"  target:     v0.0.2 (update available)\n",
		"  last-good:  v0.0.1\n",
		"  quarantine: v0.0.3\n",
		"\n  alpha-pq-authority ",
		"\n  courier ",
	} {
		if !strings.Contains(goOut, want) {
			t.Errorf("status output is missing %q", want)
		}
	}
	if strings.Contains(goOut, "ghost-pq-mixserver ") {
		t.Error("a service outside the topology must not get a state line")
	}
	t.Logf("status output:\n%s", goOut)
}

func TestUnitNames(t *testing.T) {
	needTools(t, "git")
	newFixture(t)
	e, err := track.LoadEnv()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"alpha-pq-authority": "kpauthority@alpha-pq-authority",
		"alpha-pq-mixserver": "kpserver@alpha-pq-mixserver",

		"alpha-pq-storagenode-0": "kpreplica@alpha-pq-storagenode-0",
		"courier":                "courier",

		"ghost-pq-mixserver": "ERR",
	}
	if len(want) != len(fixtureServices) {
		t.Fatalf("the table has %d services and the fixture has %d",
			len(want), len(fixtureServices))
	}
	for _, svc := range fixtureServices {
		got, err := unitOf(e, svc)
		if err != nil {
			got = "ERR"
		}
		if got != want[svc] {
			t.Errorf("unit for %s: got %q, want %q", svc, got, want[svc])
		}
		t.Logf("%-24s unit=%s state=%s", svc, got, stateOrSkip(got))
	}
}

func stateOrSkip(unit string) string {
	if unit == "ERR" {
		return "-"
	}
	return unitState(verify.Platform(), unit)
}

func TestStatusDegrades(t *testing.T) {
	needTools(t, "git")

	t.Run("no services resolve for this host", func(t *testing.T) {
		f := newFixture(t)
		write(t, f.toml, twoHostTOML)
		t.Setenv("KNO_HOST", "")
		goOut, _, _ := runGo(t)

		if strings.Contains(goOut, "(none selected") {
			t.Errorf("a push-only machine should list the services it organizes:\n%s", goOut)
		}
		for _, want := range []string{"it organizes the hosts above",
			"remote-a (root@remote-a)", "remote-b (root@remote-b)"} {
			if !strings.Contains(goOut, want) {
				t.Errorf("a push-only machine must say which hosts it organizes (%q):\n%s", want, goOut)
			}
		}
	})

	t.Run("no systemctl on PATH", func(t *testing.T) {
		newFixture(t)
		t.Setenv("PATH", minimalPath(t))
		if _, err := exec.LookPath("systemctl"); err == nil {
			t.Skip("systemctl still resolves; cannot test the degraded path")
		}
		goOut, _, _ := runGo(t)
		if !strings.Contains(goOut, "  (systemctl not present; states unavailable)\n") {
			t.Errorf("missing the no-systemctl note:\n%s", goOut)
		}
	})
}

func minimalPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	seen := map[string]bool{"systemctl": true, "nc": true}
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if d == "" {
			continue
		}
		ents, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range ents {
			name := e.Name()
			if seen[name] {
				continue
			}
			seen[name] = true

			_ = os.Symlink(filepath.Join(d, name), filepath.Join(dir, name))
		}
	}
	return dir
}

func TestHostRowsReadTheTicksOwnState(t *testing.T) {
	dir := t.TempDir()
	e := track.Env{StateDir: dir}

	if got := hostTick(e, "mixy"); got.current != "" || got.state != "" {
		t.Errorf("a host the tick never ran for reported state: %+v", got)
	}

	h := knopath.HostState(dir, "mixy")
	if err := os.MkdirAll(h, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"current": "v0.0.104\n",
		"state":   "pending\n",
		"pending": "tag=v0.0.104\nprev=v0.0.102\ndeadline=1700000000\n",
	} {
		if err := os.WriteFile(filepath.Join(h, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := hostTick(e, "mixy")
	if got.current != "v0.0.104" || got.state != "pending" {
		t.Errorf("hostTick = %+v, want the release and the state", got)
	}

	if other := hostTick(e, "reed"); other.state != "" {
		t.Errorf("mixy's state leaked to reed: %+v", other)
	}
}

func TestNarrowKeepsOnlyTheHostsNamed(t *testing.T) {
	e := track.Env{
		ConfigPath: "/tmp/kno.toml",
		Config: config.Config{Hosts: map[string]config.Host{
			"annares": {SSH: "root@power9"},
			"mixy":    {SSH: "root@mixy"},
			"reed":    {SSH: "root@reed"},
		}},
		Managed: []track.ManagedService{
			{Host: "annares", Service: "annares-pq-authority"},
			{Host: "mixy", Service: "mixy-pq-mixserver"},
			{Host: "reed", Service: "reed-pq-mixserver"},
		},
	}
	got, err := narrow(e, "annares")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Config.Hosts) != 1 {
		t.Errorf("hosts = %v, want only annares", got.Config.Hosts)
	}

	if len(got.Managed) != 1 || got.Managed[0].Host != "annares" {
		t.Errorf("managed = %v, want only annares", got.Managed)
	}

	two, err := narrow(e, "mixy,reed")
	if err != nil {
		t.Fatal(err)
	}
	if len(two.Config.Hosts) != 2 || len(two.Managed) != 2 {
		t.Errorf("two hosts: hosts=%v managed=%v", two.Config.Hosts, two.Managed)
	}
}

func TestNarrowRefusesAnUnknownHost(t *testing.T) {
	e := track.Env{Config: config.Config{Hosts: map[string]config.Host{"mixy": {}}}}
	if _, err := narrow(e, "nosuch"); err == nil {
		t.Fatal("an unknown host must be refused")
	}
	if _, err := narrow(e, "mixy,nosuch"); err == nil {
		t.Fatal("one unknown host among known ones must still be refused")
	}
	if _, err := narrow(e, "mixy"); err != nil {
		t.Fatalf("a known host must work: %v", err)
	}
}

func TestClientDaemonIsAService(t *testing.T) {
	no := false
	repo := t.TempDir()
	c := config.Config{Hosts: map[string]config.Host{
		"lilly": {SSH: "root@lilly", Services: []string{"lilly-pq-servicenode"},
			KpclientdUser: "kpclient"},
		"mixy":  {SSH: "root@mixy", Services: []string{"mixy-pq-mixserver"}},
		"quiet": {SSH: "root@quiet", Services: []string{"quiet-pq-mixserver"}, Kpclientd: &no},
	}}
	e := track.Env{Repo: repo, Network: "testnet", ConfigPath: filepath.Join(repo, "kno.toml")}
	e.ApplyConfigForTest(c)

	byHost := map[string][]string{}
	for _, m := range e.Managed {
		byHost[m.Host] = append(byHost[m.Host], m.Service)
	}
	for _, host := range []string{"lilly", "mixy"} {
		if !slices.Contains(byHost[host], service.Client) {
			t.Errorf("%s runs the client daemon and does not list it: %v", host, byHost[host])
		}
	}

	if slices.Contains(byHost["quiet"], service.Client) {
		t.Errorf("a host that runs no client daemon lists one: %v", byHost["quiet"])
	}

	if stateKey("lilly", service.Client) == stateKey("mixy", service.Client) {
		t.Error("two hosts' client daemons share one state key")
	}
}

func TestAHostAtTheTargetIsPendingOnlyForNewConfigs(t *testing.T) {
	dir := t.TempDir()
	e := track.Env{StateDir: dir, Repo: t.TempDir(), Network: "namenlos",
		Config: config.Config{Hosts: map[string]config.Host{
			"mixy": {SSH: "root@mixy", Services: []string{"mixy-pq-mixserver"}},
		}}}
	if got := configGap(e, "mixy"); got != nil {
		t.Errorf("a host with no record of its configs was reported: %v", got)
	}
	h := knopath.HostState(dir, "mixy")
	if err := os.MkdirAll(h, 0o755); err != nil {
		t.Fatal(err)
	}
	same := "mixy-pq-mixserver=" + deb.ConfigStamp(e.Repo, e.Network, "mixy-pq-mixserver") + "\n"
	if err := os.WriteFile(filepath.Join(h, "configs"), []byte(same), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := configGap(e, "mixy"); len(got) != 0 {
		t.Errorf("a host with the configs this repo builds is pending: %v", got)
	}
	if err := os.WriteFile(filepath.Join(h, "configs"), []byte("mixy-pq-mixserver=+19990101000000.gold\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := configGap(e, "mixy"); len(got) != 1 || got[0] != "mixy-pq-mixserver" {
		t.Errorf("a config change at the target is not reported: %v", got)
	}
}

func TestAHostIsAskedForTheUnitsOfItsOwnRelease(t *testing.T) {
	dir := t.TempDir()
	e := track.Env{StateDir: dir}
	s := service.Service{Unit: "kpserver@lilly-pq-servicenode", LegacyUnit: "pq-katzenpost-mixserver"}
	if got := unitOn(e, "lilly", s); got != s.Unit {
		t.Errorf("a host with no state: %q, want the current unit", got)
	}
	h := knopath.HostState(dir, "lilly")
	if err := os.MkdirAll(h, 0o755); err != nil {
		t.Fatal(err)
	}
	for cur, want := range map[string]string{"v0.0.104\n": s.LegacyUnit, "v0.0.107\n": s.Unit} {
		if err := os.WriteFile(filepath.Join(h, "current"), []byte(cur), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := unitOn(e, "lilly", s); got != want {
			t.Errorf("a host on %s is asked for %q, want %q", strings.TrimSpace(cur), got, want)
		}
	}
}

func TestServicesAreListedByHostWithTheClientLast(t *testing.T) {
	m := map[string][]track.ManagedService{
		"root@power9-internal": {{Host: "annares", Service: service.Client}, {Host: "annares", Service: "annares-pq-servicenode"}, {Host: "annares", Service: "annares-pq-authority"}},
		"root@lilly":           {{Host: "lilly", Service: service.Client}, {Host: "lilly", Service: "lilly-pq-servicenode"}},
		"root@haywood":         {{Host: "haywood", Service: "haywood-pq-gatewaynode"}, {Host: "haywood", Service: service.Client}},
	}
	got := sortedNodes(m)
	if strings.Join(got, " ") != "root@power9-internal root@haywood root@lilly" {
		t.Errorf("hosts are in %v, want label order: annares, haywood, lilly", got)
	}
	for node, svcs := range m {
		if svcs[len(svcs)-1].Service != service.Client {
			t.Errorf("%s: the client is not last: %v", node, svcs)
		}
	}
	if m["root@power9-internal"][0].Service != "annares-pq-authority" {
		t.Errorf("annares services are not sorted: %v", m["root@power9-internal"])
	}
}

func TestStagedPackagesAreCountedByRelease(t *testing.T) {
	got := stagedSummary([]string{
		"katzenpost_0.0.104+ubuntu24.04_amd64.deb", "kno_0.0.104-262_amd64.deb",
		"katzenpost_0.0.107+ubuntu24.04_amd64.deb", "kno_0.0.107-1+20261021000047.gbe621db_amd64.deb",
		"namenlos-config-reed-pq-mixserver_0.0.107-1+20260701154849.gdf160a9_amd64.deb",
	})
	if !strings.HasPrefix(got, "0.0.104 (2), 0.0.107 (3)") {
		t.Errorf("stagedSummary = %q", got)
	}
}

func TestPackagesShowTheReleasesHostsUse(t *testing.T) {
	dir := t.TempDir()
	e := track.Env{StateDir: dir, Config: config.Config{Hosts: map[string]config.Host{"reed": {SSH: "root@reed"}}}}
	h := knopath.HostState(dir, "reed")
	if err := os.MkdirAll(h, 0o755); err != nil {
		t.Fatal(err)
	}
	for f, v := range map[string]string{"current": "v0.0.107\n", "last-known-good": "v0.0.104\n"} {
		if err := os.WriteFile(filepath.Join(h, f), []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	in := releasesInUse(e)
	if !in["v0.0.107"] || !in["v0.0.104"] || in["v0.0.105"] {
		t.Errorf("releasesInUse = %v", in)
	}
}
