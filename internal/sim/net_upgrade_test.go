package sim

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/knopath"
)

func rehearsalRun(t *testing.T) (*run, []daemon) {
	t.Helper()
	r := testRun(t)
	r.Emulate = true
	svcs, err := r.deriveSpec()
	if err != nil {
		t.Fatal(err)
	}
	return r, svcs
}

func TestTheRehearsalCoversTheOperatorsThreePlatforms(t *testing.T) {
	r, svcs := rehearsalRun(t)
	r.Rehearse, r.Pull, r.Fail = "reed,mixy,annares", "annares", "reed"
	hs, err := r.rehearsals(svcs)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"reed":    "ubuntu-24.04/amd64",
		"mixy":    "ubuntu-25.04/amd64",
		"annares": "ubuntu-26.04/ppc64le",
	}
	for _, h := range hs {
		if got := h.P.String(); got != want[h.Node] {
			t.Errorf("%s rehearses on %s, want %s", h.Node, got, want[h.Node])
		}
	}
	if !hs[2].Pull || hs[0].Pull || !hs[0].Fail || hs[1].Fail {
		t.Errorf("pull and fail are on the wrong nodes: %+v", hs)
	}
	if len(hs[2].Svcs) != 4 {
		t.Errorf("annares rehearses %d services, want its 4", len(hs[2].Svcs))
	}
}

func TestTheRehearsalRefusesANodeThatSharesItsAddress(t *testing.T) {
	r, svcs := rehearsalRun(t)
	r.Rehearse = "wauland"
	_, err := r.rehearsals(svcs)
	if err == nil || !strings.Contains(err.Error(), "shares its address") {
		t.Fatalf("wauland shares a container with other nodes, and got: %v", err)
	}
}

func TestTheRehearsalNeedsThePreviousRelease(t *testing.T) {
	r := newRun(NetOptions{Net: "t", Work: t.TempDir(), Out: os.Stderr})
	err := r.upgradeStage()
	if err == nil || !strings.Contains(err.Error(), "--from") {
		t.Fatalf("an upgrade with no previous release: %v", err)
	}
}

func TestVersionIsMatchesTheTagNotAPrefix(t *testing.T) {
	cases := []struct {
		v, tag string
		want   bool
	}{
		{"0.0.105+ubuntu25.04", "v0.0.105", true},
		{"0.0.104+ubuntu26.04", "v0.0.104", true},
		{"0.0.1050+ubuntu25.04", "v0.0.105", false},
		{"0.0.104+ubuntu25.04", "v0.0.105", false},
	}
	for _, c := range cases {
		if got := versionIs(c.v, c.tag); got != c.want {
			t.Errorf("versionIs(%q, %q) = %v", c.v, c.tag, got)
		}
	}
}

func TestRekeyTakesThePackagesOwnServiceConfig(t *testing.T) {
	r := testRun(t)
	got, legacy := r.renderedFor("mixy-pq-mixserver", "/x/etc/pq-katzenpost-mixserver/katzenpost.toml", "/x")
	if want := r.cfg + "/mixy-pq-mixserver.toml"; got != want {
		t.Errorf("mixy's package gets %s, want %s", got, want)
	}
	if !legacy {
		t.Error("mixy's v0.0.104 config path was not recognised as the old layout")
	}
	if got, _ := r.renderedFor("annares-pq-servicenode", "/x/etc/pq-katzenpost-mixserver/courier.toml", "/x"); got != r.cfg+"/courier.toml" {
		t.Errorf("the courier config gets %s", got)
	}
	if got, _ := r.renderedFor("mixy-pq-mixserver", "/x/etc/other.toml", "/x"); got != "" {
		t.Errorf("an unrelated toml gets %s", got)
	}
}

func TestAUnitIsSteadyOnlyWhenActiveWithNoRestart(t *testing.T) {
	if !steady("ActiveState=active\nNRestarts=0\n", "ActiveState=active\nNRestarts=0\n") {
		t.Error("an active unit with no restart is not steady")
	}
	if steady("ActiveState=active\nNRestarts=3\n", "ActiveState=active\nNRestarts=4\n") {
		t.Error("a unit that restarted between samples is steady")
	}
	if steady("ActiveState=active\nNRestarts=0\n", "ActiveState=activating\nNRestarts=0\n") {
		t.Error("an activating unit is steady")
	}
}

func TestAHostGetsItsOwnIPv6AddressesOnly(t *testing.T) {
	lines := []string{
		"[Server]",
		`  Addresses = ["tcp://185.193.125.85:443", "tcp://[2a0a:3840:1337:125:0:b9c1:7d55:1337]:443"]`,
		`  BindAddresses = ["tcp://[2a01:4f9:3100:4b60:1013::1]:1984"]`,
		"[[Authorities]]",
		`  Addresses = ["tcp://[2001:db8::9]:1"]`,
	}
	got := strings.Join(ownV6(lines), " ")
	if got != "2a0a:3840:1337:125:0:b9c1:7d55:1337 2a01:4f9:3100:4b60:1013::1" {
		t.Errorf("ownV6 = %q", got)
	}
}

func TestLogDirsAreTheDirectoriesOfLoggingFiles(t *testing.T) {
	lines := []string{
		"[Logging]", `  File = "/var/log/katzenpost/courier.log"`, `  Level = "INFO"`,
		"[Server]", `  File = ""`, `  DataDir = "/var/lib/x"`,
	}
	got := strings.Join(logDirs(lines), " ")
	if got != "/var/log/katzenpost" {
		t.Errorf("logDirs = %q", got)
	}
}

func TestTheRehearsalCheckoutIsAModuleWithTheSimsNetwork(t *testing.T) {
	r := testRun(t)
	r.Repo, _ = filepath.Abs("../../data")
	if err := copyTree(knopath.NetworkDir(r.Repo, "namenlos"), r.net, knopath.GeneratedName); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.net, "marker"), []byte("sim"), 0o644); err != nil {
		t.Fatal(err)
	}
	u := upgradeEnv{prev: "v0.0.104", next: "v0.0.105",
		root: filepath.Join(r.Work, "checkout")}
	u.repo = filepath.Join(u.root, "data")
	if err := r.simRepo(u); err != nil {
		t.Fatal(err)
	}
	gen := "data/networks/namenlos/" + knopath.GeneratedName + "/"
	for _, f := range []string{"go.mod", "cmd/kno/main.go", "data/networks/namenlos/marker", ".git",
		gen + "configs/mixy-pq-mixserver.toml", gen + "services/mixy-pq-mixserver.conf", gen + "keys", gen + "network.toml"} {
		if _, err := os.Stat(filepath.Join(u.root, f)); err != nil {
			t.Errorf("the checkout lacks %s: %v", f, err)
		}
	}
}

func TestTheNodesKnoUsesTheDirectoriesTheRehearsalReads(t *testing.T) {
	r := testRun(t)
	u := upgradeEnv{nodeArt: "/x/node-artifacts", nodeConf: "/x/node", repo: "/x/checkout/data", kp: "/x/kp"}
	env := strings.Join(r.nodeEnv(u, rehearsal{Node: "annares"}), "\n")
	for _, want := range []string{
		"RELEASES_DIR=/x/node-artifacts/releases",
		"UPDATER_STATE_DIR=/x/node-artifacts/state",
		"GIT_AUTHOR_EMAIL=",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("the node's kno env lacks %q: the installed kno otherwise uses /var/lib/kno-updater", want)
		}
	}
}

func TestTheNodesVerifyCanFindItsFetchTool(t *testing.T) {
	r := testRun(t)
	env := r.nodeEnv(upgradeEnv{nodeArt: "/x", nodeConf: "/x", repo: "/x", kp: "/x"}, rehearsal{Node: "annares"})
	for _, kv := range env {
		if strings.HasPrefix(kv, "KNO_FETCH=") {
			t.Fatalf("%s: kno verify reads KNO_FETCH as the path of the fetch tool", kv)
		}
	}
}

func TestRekeyKeepsTheOldPackagesPaths(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "new.toml")
	dst := filepath.Join(dir, "old.toml")
	newCfg := "[Server]\n  DataDir = \"/var/lib/katzenpost/server/a\"\n  Identifier = \"fresh\"\n" +
		"[[Plugin]]\n    Command = \"/usr/bin/kpcourier\"\n      c = \"/etc/katzenpost/courier/courier.toml\"\n"
	oldCfg := "[Server]\n  DataDir = \"/var/lib/pq-katzenpost-mixserver\"\n  Identifier = \"real\"\n" +
		"[[Plugin]]\n    Command = \"/usr/local/bin/pq-katzenpost-courier\"\n      c = \"/etc/pq-katzenpost-mixserver/courier.toml\"\n"
	if err := os.WriteFile(src, []byte(newCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte(oldCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyKeepingPaths(src, dst, 0o644); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(dst)
	got := string(b)
	for _, want := range []string{`Identifier = "fresh"`, `DataDir = "/var/lib/pq-katzenpost-mixserver"`,
		`Command = "/usr/local/bin/pq-katzenpost-courier"`, `c = "/etc/pq-katzenpost-mixserver/courier.toml"`} {
		if !strings.Contains(got, want) {
			t.Errorf("rekeyed old config lacks %s:\n%s", want, got)
		}
	}
}

func TestAnEmulatedNodeGivesFetchTime(t *testing.T) {
	r := testRun(t)
	other := "ppc64le"
	if runtime.GOARCH == other {
		other = "amd64"
	}
	env := strings.Join(r.nodeEnv(upgradeEnv{}, rehearsal{Node: "annares", P: platform{Goarch: other}}), "\n")
	if !strings.Contains(env, "VERIFY_FETCH_TIMEOUT=600") {
		t.Error("an emulated node runs kpfetch several times slower; its verify keeps the 120s default and times out")
	}
	native := strings.Join(r.nodeEnv(upgradeEnv{}, rehearsal{Node: "reed", P: platform{Goarch: runtime.GOARCH}}), "\n")
	if strings.Contains(native, "VERIFY_FETCH_TIMEOUT") {
		t.Error("a native node should verify with the default fetch timeout, as a real host does")
	}
}

func TestTheRehearsalRefusesAFailOrPullNodeItDoesNotUpgrade(t *testing.T) {
	r, svcs := rehearsalRun(t)
	for _, c := range []struct{ rehearse, pull, fail, want string }{
		{"mixy,annares", "annares", "reed", "--fail names reed"},
		{"mixy,annares", "reed", "", "--pull names reed"},
		{"reed,annares", "annares", "annares", "--fail and --pull both name annares"},
	} {
		r.Rehearse, r.Pull, r.Fail = c.rehearse, c.pull, c.fail
		_, err := r.rehearsals(svcs)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("--rehearse %s --pull %s --fail %s: got %v, want %q",
				c.rehearse, c.pull, c.fail, err, c.want)
		}
	}
	r.Rehearse, r.Pull, r.Fail = "mixy,annares", "annares", ""
	if _, err := r.rehearsals(svcs); err != nil {
		t.Errorf("--fail '' turns the rollback off, and got: %v", err)
	}
}
