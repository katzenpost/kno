package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

func TestWriteKeepsThePeers(t *testing.T) {
	p := filepath.Join(t.TempDir(), "kno.toml")
	in := Config{Network: "namenlos", Nodes: []string{"mixy"},
		Peers: map[string]string{"reed": "tcp://10.0.0.2:4242", "annares": "tcp://10.0.0.1:4242"}}
	if err := Write(in, p); err != nil {
		t.Fatal(err)
	}
	out, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Peers) != 2 || out.Peers["reed"] != in.Peers["reed"] || out.Peers["annares"] != in.Peers["annares"] {
		t.Errorf("Peers after Write and Load = %v, want %v", out.Peers, in.Peers)
	}
}

func writeConf(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "kno.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func wantWarning(t *testing.T, warns []string, substr string) {
	t.Helper()
	for _, w := range warns {
		if strings.Contains(w, substr) {
			return
		}
	}
	t.Errorf("no warning mentions %q: %q", substr, warns)
}

func TestLoadWarnsAboutAKeyItDoesNotKnowAndStillLoads(t *testing.T) {
	p := writeConf(t, "network = \"namenlos\"\nretired_setting = \"yes\"\n\n"+
		"[hosts.\"mixy\"]\nscheduler = \"timer\"\nservics = [\"mixy-pq-mixserver\"]\n")
	c, warns, err := load(p)
	if err != nil {
		t.Fatalf("a kno.toml with a key kno no longer knows did not load: %v", err)
	}
	if c.Network != "namenlos" || c.Hosts["mixy"].Scheduler != "timer" {
		t.Errorf("the known keys did not load: %+v", c)
	}
	wantWarning(t, warns, "unknown key retired_setting")
	wantWarning(t, warns, "unknown key hosts.mixy.servics")
	if _, err := Load(p); err != nil {
		t.Errorf("Load: %v", err)
	}
}

func TestLoadWarnsAboutAValueKnoDoesNotKnow(t *testing.T) {
	p := writeConf(t, "[defaults]\nprivilege = \"root\"\n\n"+
		"[hosts.\"mixy\"]\ninstall = \"rpm\"\nscheduler = \"hourly\"\nprivilege = \"operator\"\n")
	_, warns, err := load(p)
	if err != nil {
		t.Fatal(err)
	}
	wantWarning(t, warns, `[defaults] privilege "root"`)
	wantWarning(t, warns, `install "rpm"`)
	wantWarning(t, warns, `scheduler "hourly"`)
	if len(warns) != 3 {
		t.Errorf("want 3 warnings (the legacy privilege operator is valid), got %q", warns)
	}
}

func TestPlacementIssuesNameAServiceWithNoConfig(t *testing.T) {
	c := Config{Network: "namenlos", Nodes: []string{"mixy"},
		Hosts: map[string]Host{"mixy": {Services: []string{"mixy-pq-mixserver", "mixy-pq-mixservr"}}}}
	got := PlacementIssues(testutil.RepoRoot(t), c)
	if len(got) != 1 || !strings.Contains(got[0], "mixy-pq-mixservr") {
		t.Errorf("PlacementIssues = %q, want one issue for mixy-pq-mixservr", got)
	}
}

func TestLocalHostPrefersTheLabelAndRefusesAGuess(t *testing.T) {
	hn, err := os.Hostname()
	if err != nil || hn == "" {
		t.Skip("this machine has no hostname")
	}
	t.Setenv("KNO_HOST", "")
	c := Config{Hosts: map[string]Host{
		hn:      {SSH: "root@a"},
		"other": {SSH: "root@b", Hostname: hn},
	}}
	for i := 0; i < 20; i++ {
		if label, _, ok := LocalHost(c); !ok || label != hn {
			t.Fatalf("LocalHost = %q, %v; want the host whose label is %s", label, ok, hn)
		}
	}
	two := Config{Hosts: map[string]Host{
		"a": {SSH: "root@a", Hostname: hn},
		"b": {SSH: "root@b", Hostname: hn},
	}}
	if label, _, ok := LocalHost(two); ok {
		t.Errorf("LocalHost chose %q of two hosts with the hostname %s", label, hn)
	}
}

func TestKnoBuildFindsKnosOwnCloneOutsideTheCheckout(t *testing.T) {
	home := t.TempDir()
	clone := filepath.Join(home, "network-organizer", "data")
	if err := os.MkdirAll(filepath.Join(clone, "networks"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KNO_REPO", "")
	t.Setenv("KNO_HOME", "")
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	if got := RepoDir(); got != clone {
		t.Errorf("RepoDir = %s, want kno's own clone %s, as kno update finds it", got, clone)
	}
}
