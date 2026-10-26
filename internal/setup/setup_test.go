package setup

import (
	"bufio"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/katzenpost/kno/internal/config"
)

func TestAskHostDefaultsToPriorSSH(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("\n"))
	ssh, hn := askHost(r, "annares", config.Host{SSH: "root@power9-internal"})
	if ssh != "root@power9-internal" {
		t.Errorf("ssh = %q, want the prior target", ssh)
	}
	if hn != "" {
		t.Errorf("hostname = %q, want empty for a remote host", hn)
	}
}

func TestAskHostDefaultsToThisHostWithoutPrior(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("\n"))
	ssh, hn := askHost(r, "mixy", config.Host{})
	if ssh != "" {
		t.Errorf("ssh = %q, want empty", ssh)
	}
	want, _ := os.Hostname()
	if hn != want {
		t.Errorf("hostname = %q, want %q", hn, want)
	}
}

func TestAskHostAnswerOverridesPrior(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("root@elsewhere\n"))
	ssh, _ := askHost(r, "mixy", config.Host{SSH: "root@power9-internal"})
	if ssh != "root@elsewhere" {
		t.Errorf("ssh = %q, want root@elsewhere", ssh)
	}
}

func TestAskHostThisHostClearsPrior(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("this host\n"))
	ssh, hn := askHost(r, "mixy", config.Host{SSH: "root@power9-internal"})
	if ssh != "" {
		t.Errorf("ssh = %q, want empty after answering 'this host'", ssh)
	}
	if hn == "" {
		t.Error("hostname not stamped for a local host")
	}
}

func TestPriorLabel(t *testing.T) {
	hosts := map[string]config.Host{
		"mixy":    {Services: []string{"mixy-pq-mixnode"}},
		"annares": {Services: []string{"haywood-pq-gatewaynode"}},
	}

	if got := priorLabel(hosts, "haywood-pq-gatewaynode"); got != "annares" {
		t.Errorf("priorLabel = %q, want annares", got)
	}
	if got := priorLabel(hosts, "reed-pq-mixnode"); got != "" {
		t.Errorf("priorLabel for an unplaced service = %q, want empty", got)
	}
	if got := priorLabel(nil, "mixy-pq-mixnode"); got != "" {
		t.Errorf("priorLabel with no prior config = %q, want empty", got)
	}
}

func TestIsOperatorConfig(t *testing.T) {
	remote := map[string]config.Host{"mixy": {SSH: "root@mixy"}}
	local := map[string]config.Host{"mixy": {Hostname: "mixy"}}
	cases := []struct {
		name  string
		nodes []string
		hosts map[string]config.Host
		want  bool
	}{
		{"five nodes over ssh", []string{"a", "b", "c", "d", "e"}, remote, true},
		{"one node over ssh", []string{"mixy"}, remote, true},
		{"two nodes, no ssh", []string{"a", "b"}, local, true},
		{"one local node", []string{"mixy"}, local, false},
		{"no prior config", nil, nil, false},
	}
	for _, c := range cases {
		if got := isOperatorConfig(c.nodes, c.hosts); got != c.want {
			t.Errorf("%s: isOperatorConfig = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRosterEntryListsEveryService(t *testing.T) {
	nodes := []string{"annares", "haywood", "lilly", "mixy", "reed"}
	svcs := []string{
		"annares-pq-authority", "annares-pq-servicenode",
		"annares-pq-storagenode-0", "annares-pq-storagenode-1",
		"haywood-pq-gatewaynode", "lilly-pq-servicenode",
		"mixy-pq-mixserver", "reed-pq-mixserver",
	}
	lines := rosterEntry(2, "annares", "   <- you?", nodes, svcs)
	if !strings.HasPrefix(lines[0], "   2) annares ") {
		t.Errorf("the number and name must stay answerable: %q", lines[0])
	}
	for _, want := range []string{"5 nodes", "8 services", "<- you?"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("head line %q does not say %q", lines[0], want)
		}
	}
	joined := strings.Join(lines, "\n")
	for _, s := range svcs {
		if !strings.Contains(joined, s) {
			t.Errorf("%s is not in the entry:\n%s", s, joined)
		}
	}
	if len(lines) < 3 {
		t.Errorf("eight service names on one line is unreadable:\n%s", joined)
	}
	for _, ln := range lines {
		if len(ln) > 78 {
			t.Errorf("line is %d columns: %q", len(ln), ln)
		}
	}
}

func TestRosterEntryDegrades(t *testing.T) {
	one := rosterEntry(4, "gnunet", "", []string{"gnunet"}, []string{"gnunet-pq-authority"})
	if !strings.Contains(one[0], "1 node, 1 service") {
		t.Errorf("head line %q, want singulars", one[0])
	}
	none := rosterEntry(1, "omelas", "", []string{"omelas"}, nil)
	if !strings.Contains(none[0], "services unknown") {
		t.Errorf("head line %q should admit it found no services", none[0])
	}
	if len(none) != 2 || !strings.Contains(none[1], "omelas") {
		t.Errorf("want the nodes as a fallback, got %q", none)
	}
}

func TestWrappedNeverSplitsAName(t *testing.T) {
	got := wrapped([]string{"aaaa", "bbbb", "cccc"}, "  ", 12)
	want := []string{"  aaaa bbbb", "  cccc"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("wrapped = %q, want %q", got, want)
	}
	if got := wrapped(nil, "  ", 12); got != nil {
		t.Errorf("wrapped(nil) = %q, want nothing", got)
	}
}

func TestMatchOperator(t *testing.T) {
	ops := map[string][]string{
		"alice": {"annares", "haywood", "lilly", "mixy", "reed"},
		"bob":   {"oneeyedman", "luscus"},
	}
	names := []string{"alice", "bob"}
	if got := matchOperator(ops, names, []string{"annares", "haywood", "lilly", "mixy", "reed"}); got != "alice" {
		t.Errorf("exact set: got %q, want alice", got)
	}

	if got := matchOperator(ops, names, []string{"annares", "mixy"}); got != "alice" {
		t.Errorf("partial set: got %q, want alice", got)
	}
	if got := matchOperator(ops, names, []string{"luscus"}); got != "bob" {
		t.Errorf("other operator: got %q, want bob", got)
	}
	if got := matchOperator(ops, names, nil); got != "" {
		t.Errorf("no claim: got %q, want empty", got)
	}
	if got := matchOperator(ops, names, []string{"nosuch"}); got != "" {
		t.Errorf("unknown node: got %q, want empty", got)
	}
}

func TestOverlayKeepsWhatSetupDoesNotAsk(t *testing.T) {
	base := config.Config{Peers: map[string]string{"reed": "root@reed"}}
	base.Defaults.Privilege = config.PrivApt
	over := config.Config{Peers: map[string]string{"mixy": "root@mixy"}}
	over.Update.Threshold = 2
	over.Defaults.Scheduler = "timer"
	over.Defaults.Kpclientd = config.Bool(false)

	got := overlay(base, over)
	if got.Peers["mixy"] != "root@mixy" || got.Peers["reed"] != "root@reed" {
		t.Errorf("peers = %v, want both", got.Peers)
	}
	if got.Update.Threshold != 2 {
		t.Errorf("threshold = %d, want 2", got.Update.Threshold)
	}
	if got.Defaults.Scheduler != "timer" || got.Defaults.Privilege != config.PrivApt ||
		got.Defaults.Kpclientd == nil || *got.Defaults.Kpclientd {
		t.Errorf("defaults = %+v, want the user's answers over the system's", got.Defaults)
	}
	if len(base.Peers) != 1 {
		t.Errorf("overlay changed its base: %v", base.Peers)
	}
	if got := overlay(config.Config{}, over); got.Peers["mixy"] != "root@mixy" {
		t.Errorf("with no system config, peers = %v", got.Peers)
	}
}

func TestARerunOnThisHostKeepsWhatItDidNotAsk(t *testing.T) {
	prior := config.Host{SSH: "root@mixy", Privilege: config.PrivKno, Install: config.InstallDocker,
		ClientJail: "kpc", Services: []string{"mixy-pq-mixserver"}, Scheduler: "cron"}
	h := thisHost(prior, "mixy", "timer", true, "katzen")
	if h.SSH != prior.SSH || h.Privilege != prior.Privilege || h.Install != prior.Install ||
		h.ClientJail != prior.ClientJail || len(h.Services) != 1 {
		t.Errorf("a re-run dropped a field it did not ask about: %+v", h)
	}
	if h.Hostname != "mixy" || h.Scheduler != "timer" || h.Kpclientd == nil || !*h.Kpclientd ||
		h.KpclientdUser != "katzen" {
		t.Errorf("the answers were not used: %+v", h)
	}
}

func TestARerunForEachHostKeepsWhatItDidNotAsk(t *testing.T) {
	prior := config.Host{SSH: "root@power9-internal", Hostname: "annares", Install: config.InstallDocker,
		ClientJail: "kpc", Scheduler: "cron", Kpclientd: config.Bool(false), KpclientdUser: "x",
		Privilege: config.PrivKno, Services: []string{"old"}}
	svcs := []string{"annares-pq-authority"}

	h := placedHost(prior, nil, "annares", "root@power9-internal", "", svcs)
	if h.Install != config.InstallDocker || h.ClientJail != "kpc" || h.Hostname != "annares" {
		t.Errorf("a re-run dropped a field it did not ask about: %+v", h)
	}
	if h.Scheduler != "" || h.Kpclientd != nil || h.KpclientdUser != "" || h.Privilege != "" {
		t.Errorf("a host that now follows the defaults kept its old answers: %+v", h)
	}
	if strings.Join(h.Services, " ") != "annares-pq-authority" || h.SSH != "root@power9-internal" {
		t.Errorf("the placement was not used: %+v", h)
	}

	odd := map[string]config.Host{"annares": func() config.Host {
		e := prior
		e.Scheduler = "timer"
		return e
	}()}
	h = placedHost(prior, odd, "annares", "root@elsewhere", "", svcs)
	if h.Scheduler != "timer" || h.Privilege != config.PrivKno || h.Install != config.InstallDocker {
		t.Errorf("an exception lost its answers or a kept field: %+v", h)
	}

	h = placedHost(config.Host{Hostname: "laptop"}, nil, "mixy", "root@mixy", "", svcs)
	if h.Hostname != "" {
		t.Errorf("a host that moved off this host kept this host's name %q", h.Hostname)
	}
	h = placedHost(prior, nil, "annares", "", "laptop", svcs)
	if h.Hostname != "laptop" || h.SSH != "" {
		t.Errorf("a host that moved to this host: %+v", h)
	}
}

func TestTheSharedRepoNoteNamesARealCommand(t *testing.T) {
	if strings.Contains(sharedRepoNote, "kno-topology-propose") ||
		!strings.Contains(sharedRepoNote, "'kno topology-propose'") {
		t.Errorf("the note names a command that does not exist:\n%s", sharedRepoNote)
	}
}

func TestTheConfigInstallUsesTheSudoOfThisAccount(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "argv.log")
	for _, n := range []string{"sudo", "mysudo"} {
		script := "#!/bin/sh\necho \"" + n + " $*\" >> " + log + "\n"
		if err := os.WriteFile(filepath.Join(dir, n), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SUDO", "mysudo")
	old := systemConfigPath
	systemConfigPath = filepath.Join(dir, "etc", "kno.toml")
	t.Cleanup(func() { systemConfigPath = old })

	src := filepath.Join(dir, "kno.toml")
	if err := os.WriteFile(src, []byte("network = \"namenlos\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sudoInstallConfig(src); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	want := "mysudo mkdir -p " + filepath.Dir(systemConfigPath) + "\n" +
		"mysudo install -m 0644 " + src + " " + systemConfigPath + "\n"
	if string(b) != want {
		t.Errorf("ran\n%s\nwant\n%s", b, want)
	}

	t.Setenv("SUDO", "")
	if err := sudoInstallConfig(src); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(systemConfigPath); err != nil || string(got) != "network = \"namenlos\"\n" {
		t.Errorf("SUDO= did not install the file directly: %q %v", got, err)
	}
}

func TestAskStopsAtTheEndOfInput(t *testing.T) {
	quit = func(code int) { panic(code) }
	t.Cleanup(func() { quit = os.Exit })
	for name, f := range map[string]func(r *bufio.Reader){
		"pick a node":     func(r *bufio.Reader) { pickNode(r, []string{"mixy", "reed"}) },
		"a privilege":     func(r *bufio.Reader) { privilege(r, "mixy", "nonsense") },
		"another node, y": func(r *bufio.Reader) { yes(ask(r, "organize another node?", "y")) },
	} {
		done := make(chan interface{}, 1)
		go func() {
			defer func() { done <- recover() }()
			f(bufio.NewReader(strings.NewReader("")))
		}()
		select {
		case got := <-done:
			if got != 1 {
				t.Errorf("%s: at the end of input, setup did %v, want exit 1", name, got)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: setup asks again forever at the end of input", name)
		}
	}
}

func TestSetupWritesTheFileKnoReadsFirst(t *testing.T) {
	if u, err := user.Current(); err == nil && u.Username == config.UpdaterAccount {
		t.Skip("the updater account does not read a home directory")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KNO_CONFIG", "")
	dest, err := defaultDest()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("network = \"namenlos\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := config.ResolvePath(""); got != dest {
		t.Errorf("setup writes %s, and kno reads %s", dest, got)
	}
}
