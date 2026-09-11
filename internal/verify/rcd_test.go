package verify

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/katzenpost/kno/internal/track"
)

type recordRunner struct{ calls []string }

func (r *recordRunner) Run(_ time.Duration, name string, args ...string) (string, int) {
	r.calls = append(r.calls, strings.Join(append([]string{name}, args...), " "))
	return "", 1
}

func TestFetchRunsInTheClientJail(t *testing.T) {
	r := &recordRunner{}
	o := Options{
		ConsensusOnly: true,
		Fetch:         "/usr/local/bin/kpfetch",
		Thinclient:    "/usr/local/etc/kpclientd/thinclient.toml",
		Wrap:          []string{"jexec", "kpclient"},
		FileRoot:      "/usr/jails/kpclient",
		Run:           r,
		Systemd:       RcD{},
	}
	verdict(track.Logger{Tool: "verify", W: io.Discard}, o)
	if len(r.calls) == 0 || !strings.HasPrefix(r.calls[0], "jexec kpclient /usr/local/bin/kpfetch -f /usr/local/etc/kpclientd/thinclient.toml") {
		t.Fatalf("kpfetch did not run in the client jail: %v", r.calls)
	}
}

func TestAMissingClientJailIsALocalFault(t *testing.T) {
	o := Options{Fetch: "/usr/local/bin/kpfetch", Wrap: []string{"jexec", "kpclient"}}
	why := cannotCheck(o, 1)
	if !strings.Contains(why, "the jail kpclient for kpclientd does not exist") {
		t.Fatalf("a missing client jail must not read as a network without consensus: %q", why)
	}
}

func TestJailUnit(t *testing.T) {
	if got := JailUnit("respectmy", "katzenpost"); got != "respectmy/katzenpost" {
		t.Errorf("JailUnit = %s", got)
	}
	if got := JailUnit("", "kpserver@x"); got != "kpserver@x" {
		t.Errorf("a systemd unit changed: %s", got)
	}
}

func jailRoot(t *testing.T, tools ...string) string {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "usr", "local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range tools {
		if err := os.WriteFile(filepath.Join(bin, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestTheJailedFetchToolIsLookedUpInTheJail(t *testing.T) {
	t.Setenv("KNO_PING", "")
	t.Setenv("KNO_CPBENCH", "")
	var o Options
	inJail(&o, "kpclient", jailRoot(t, "pq-katzenpost-fetch"))
	if o.Fetch != "/usr/local/bin/pq-katzenpost-fetch" {
		t.Errorf("fetch = %q, want the tool that the jail has", o.Fetch)
	}

	r := &recordRunner{}
	o = Options{ConsensusOnly: true, Thinclient: "/t.toml", Run: r, Systemd: RcD{}}
	inJail(&o, "kpclient", jailRoot(t))
	if o.Fetch != "" {
		t.Fatalf("fetch = %q, but the jail has no fetch tool", o.Fetch)
	}
	if code := verdict(track.Logger{Tool: "verify", W: io.Discard}, o); code != CodeCannotAsk {
		t.Errorf("exit = %d, want %d: a missing fetch tool is a fault on this host", code, CodeCannotAsk)
	}
	if len(r.calls) != 0 {
		t.Errorf("verify ran %v with no fetch tool", r.calls)
	}
}

func TestTheJailKeepsTheDataPlaneToolsThatTheOperatorSet(t *testing.T) {
	t.Setenv("KNO_PING", "/opt/kpping")
	t.Setenv("KNO_CPBENCH", "/opt/cp-bench")
	var o Options
	inJail(&o, "kpclient", jailRoot(t, "kpfetch"))
	if o.Ping != "/opt/kpping" || o.CPBench != "/opt/cp-bench" {
		t.Errorf("ping %q, cp-bench %q: the jail replaced KNO_PING or KNO_CPBENCH", o.Ping, o.CPBench)
	}

	t.Setenv("KNO_PING", "")
	t.Setenv("KNO_CPBENCH", "")
	inJail(&o, "kpclient", jailRoot(t, "kpfetch", "kpping", "cp-bench"))
	if o.Ping != "/usr/local/bin/kpping" || o.CPBench != "/usr/local/bin/cp-bench" {
		t.Errorf("ping %q, cp-bench %q: the tools in the jail were not found", o.Ping, o.CPBench)
	}
}

func TestTheThinclientVariableIsAPathInTheJail(t *testing.T) {
	t.Setenv("VERIFY_THINCLIENT", "")
	if got := Thinclient(true); got != "/usr/local/etc/kpclientd/thinclient.toml" {
		t.Errorf("jailed default = %q", got)
	}
	t.Setenv("VERIFY_THINCLIENT", "/usr/local/etc/other.toml")
	if got := Thinclient(true); got != "/usr/local/etc/other.toml" {
		t.Errorf("Thinclient = %q, want the variable", got)
	}
}

func TestAJailDaemonIsActiveWhileItsChildPidRuns(t *testing.T) {
	root := t.TempDir()
	saved := jlsPath
	t.Cleanup(func() { jlsPath = saved })
	jlsPath = func(string) string { return root }
	if alive, known := jailDaemon("respectmy", "katzenpost"); alive || known {
		t.Errorf("no pidfile: alive=%v known=%v, want an unknown layout", alive, known)
	}
	pidfile := filepath.Join(root, "var", "run", "katzenpost.child.pid")
	if err := os.MkdirAll(filepath.Dir(pidfile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidfile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if alive, known := jailDaemon("respectmy", "katzenpost"); !alive || !known {
		t.Errorf("a running child: alive=%v known=%v", alive, known)
	}
	if err := os.WriteFile(pidfile, []byte("999999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if alive, known := jailDaemon("respectmy", "katzenpost"); alive || !known {
		t.Errorf("a dead child: alive=%v known=%v", alive, known)
	}
	jlsPath = func(string) string { return "" }
	if alive, known := jailDaemon("respectmy", "katzenpost"); alive || !known {
		t.Errorf("no jail: alive=%v known=%v", alive, known)
	}
}
