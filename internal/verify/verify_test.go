package verify

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/katzenpost/kno/internal/knopath"

	"github.com/katzenpost/kno/internal/testutil"
	"github.com/katzenpost/kno/internal/track"
)

func TestClassifyUnit(t *testing.T) {
	const unit = "pq-katzenpost-mixserver"
	cases := []struct {
		name string
		in   UnitState
		code int
		msg  string
	}{
		{"active", UnitState{unit, false, "active", -1, true}, CodeHealthy, ""},
		{"state unavailable", UnitState{unit, false, "", -1, true}, CodeHealthy, ""},
		{"is-failed wins over an active state", UnitState{unit, true, "active", 0, true},
			CodeFailed, unit + " is failed"},
		{"failed", UnitState{unit, true, "failed", -1, true}, CodeFailed, unit + " is failed"},
		{"activating is transient", UnitState{unit, false, "activating", -1, true},
			CodeNotYet, unit + " is activating (transient)"},
		{"deactivating is transient", UnitState{unit, false, "deactivating", -1, true},
			CodeNotYet, unit + " is deactivating (transient)"},
		{"reloading is transient", UnitState{unit, false, "reloading", -1, true},
			CodeNotYet, unit + " is reloading (transient)"},
		{"inactive", UnitState{unit, false, "inactive", -1, true}, CodeFailed, unit + " is inactive"},
		{"unknown state", UnitState{unit, false, "unknown", -1, true}, CodeFailed, unit + " is unknown"},
		{"restarts at the limit", UnitState{unit, false, "active", 3, true}, CodeHealthy, ""},
		{"restarts over the limit", UnitState{unit, false, "active", 4, true},
			CodeFailed, unit + " restarted 4 times. The limit is 3."},
		{"unparsable NRestarts is ignored", UnitState{unit, false, "active", -1, true}, CodeHealthy, ""},
		{"a crash loop between two starts is a failure", UnitState{unit, false, "activating", 9, true},
			CodeFailed, unit + " restarted 9 times. The limit is 3."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, msg := ClassifyUnit(c.in, 3)
			if code != c.code || msg != c.msg {
				t.Fatalf("ClassifyUnit(%+v) = (%d, %q), want (%d, %q)", c.in, code, msg, c.code, c.msg)
			}
		})
	}
}

const participationDump = `{
  "Epoch": 1,
  "Layers": [["wauland", "wauland2"]],
  "Signers": ["annares", "luscus"]
}
`

func TestParticipating(t *testing.T) {
	cases := []struct {
		name string
		ids  []NodeIdentifier
		ok   bool
		bad  string
	}{
		{"node in the body", []NodeIdentifier{{"node", "wauland"}}, true, ""},
		{"node absent", []NodeIdentifier{{"node", "windfall"}}, false, "windfall"},
		{"authority among the signers", []NodeIdentifier{{"authority", "luscus"}}, true, ""},
		{"authority not signing", []NodeIdentifier{{"authority", "wauland"}}, false, "wauland"},
		{"a signer is not a node", []NodeIdentifier{{"node", "luscus"}}, false, "luscus"},
		{"any matches the signature block", []NodeIdentifier{{"any", "luscus"}}, true, ""},
		{"any matches the body", []NodeIdentifier{{"any", "wauland2"}}, true, ""},
		{"any still has to appear", []NodeIdentifier{{"any", "nowhere"}}, false, "nowhere"},
		{"all of several", []NodeIdentifier{{"node", "wauland"}, {"authority", "annares"}}, true, ""},
		{"one of several missing", []NodeIdentifier{{"node", "wauland"}, {"authority", "wauland"}}, false, "wauland"},
		{"no identifiers is vacuously true", nil, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bad, ok := Participating(participationDump, c.ids)
			if ok != c.ok || bad.ID != c.bad {
				t.Fatalf("Participating(%v) = (%q, %v), want (%q, %v)", c.ids, bad.ID, ok, c.bad, c.ok)
			}
		})
	}
}

func TestParticipatingWholeWord(t *testing.T) {
	dump := "{\n\"Gateways\": [\"waulandgateway\"], \"ServiceNodes\": [\"wauland2\"], \"Signers\": [\"annares\"]}\n"
	if _, ok := Participating(dump, []NodeIdentifier{{"node", "wauland"}}); ok {
		t.Error("wauland2/waulandgateway must not satisfy wauland")
	}
	if _, ok := Participating(dump, []NodeIdentifier{{"node", "wauland2"}}); !ok {
		t.Error("wauland2 should match itself")
	}
}

type call struct {
	name string
	args []string
}

type fakeRunner struct {
	calls   []call
	out     map[string]string
	rc      map[string]int
	missing map[string]bool
}

func (f *fakeRunner) Run(_ time.Duration, name string, args ...string) (string, int) {
	f.calls = append(f.calls, call{name, args})
	if f.missing[name] {
		return "", 127
	}
	return f.out[name], f.rc[name]
}

func (f *fakeRunner) ran(name string) *call {
	for i := range f.calls {
		if f.calls[i].name == name {
			return &f.calls[i]
		}
	}
	return nil
}

type fakeSystemd struct {
	available bool
	states    map[string]UnitState
	asked     []string
}

func (f *fakeSystemd) Available() bool { return f.available }

func (f *fakeSystemd) State(unit string) UnitState {
	f.asked = append(f.asked, unit)
	if s, ok := f.states[unit]; ok {
		return s
	}
	return UnitState{Unit: unit, Active: "active", Restarts: -1}
}

const fetchBin, pingBin, cpbenchBin, journalBin = "/simbin/fetch", "/simbin/ping", "/simbin/cp-bench", "/usr/bin/journalctl"

func simOptions(r *fakeRunner, s *fakeSystemd) Options {
	return Options{
		Generic:        true,
		Dataplane:      true,
		Thinclient:     "/sim/thinclient.toml",
		Fetch:          fetchBin,
		FetchTimeout:   120 * time.Second,
		ReadyTimeout:   "2m",
		Echo:           true,
		EchoService:    "echo",
		Ping:           pingBin,
		PingTimeout:    600 * time.Second,
		PingCount:      "3",
		CPBench:        cpbenchBin,
		CPBenchTimeout: 300 * time.Second,
		CPBenchPayload: "256",
		MaxRestarts:    3,
		Units:          func() ([]string, error) { return []string{"pq-katzenpost-mixserver"}, nil },
		Identifiers:    func() []NodeIdentifier { return nil },
		Run:            r,
		Systemd:        s,
	}
}

func okRunner() *fakeRunner {
	return &fakeRunner{
		out: map[string]string{fetchBin: participationDump},
		rc:  map[string]int{},
	}
}

func TestVerifySimMode(t *testing.T) {
	r, s := okRunner(), &fakeSystemd{available: true}
	var out bytes.Buffer
	if rc := Verify(&out, simOptions(r, s)); rc != CodeHealthy {
		t.Fatalf("exit = %d, want 0:\n%s", rc, out.String())
	}
	if len(s.asked) != 0 {
		t.Errorf("generic mode consulted systemd for %v", s.asked)
	}
	want := map[string][]string{
		fetchBin: {"-f", "/sim/thinclient.toml", "-l", "ERROR", "--format", "json"},
		pingBin:  {"-c", "/sim/thinclient.toml", "-s", "echo", "--thin", "-n", "3"},
	}
	for name, args := range want {
		c := r.ran(name)
		if c == nil {
			t.Fatalf("%s was never run; calls=%+v", name, r.calls)
		}
		if !reflect.DeepEqual(c.args, args) {
			t.Errorf("%s argv = %v, want %v", name, c.args, args)
		}
	}
	if r.ran(cpbenchBin) != nil {
		t.Error("cp-bench must stay off unless VERIFY_PIGEONHOLE is set")
	}
	for _, want := range []string{"consensus present", "data-plane: echo ping ok"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
}

func TestVerifyPigeonhole(t *testing.T) {
	r, s := okRunner(), &fakeSystemd{}
	o := simOptions(r, s)
	o.Pigeonhole = true
	var out bytes.Buffer
	if rc := Verify(&out, o); rc != CodeHealthy {
		t.Fatalf("exit = %d, want 0:\n%s", rc, out.String())
	}
	c := r.ran(cpbenchBin)
	if c == nil {
		t.Fatalf("cp-bench was never run; calls=%+v", r.calls)
	}
	want := []string{"-config", "/sim/thinclient.toml", "-payload", "256", "-metrics", "", "-log-level", "ERROR"}
	if !reflect.DeepEqual(c.args, want) {
		t.Errorf("cp-bench argv = %v, want %v", c.args, want)
	}
}

func TestVerifyDataplaneOutcomes(t *testing.T) {
	cases := []struct {
		name string
		tune func(*Options, *fakeRunner)
		want int
		log  string
	}{
		{"echo ping fails", func(o *Options, r *fakeRunner) { r.rc[pingBin] = 1 },
			CodeNotYet, "data-plane: echo ping failed"},
		{"ping tool missing", func(o *Options, r *fakeRunner) { o.Ping = "" },
			CodeFailed, "data-plane: kno cannot find the ping tool (kpping)"},
		{"cp-bench fails", func(o *Options, r *fakeRunner) { o.Pigeonhole = true; r.rc[cpbenchBin] = 1 },
			CodeNotYet, "data-plane: pigeonhole round-trip failed"},
		{"cp-bench missing", func(o *Options, r *fakeRunner) { o.Pigeonhole = true; o.CPBench = "" },
			CodeFailed, "data-plane: kno cannot find the pigeonhole test tool (cp-bench)"},
		{"echo disabled skips the ping", func(o *Options, r *fakeRunner) { o.Echo = false },
			CodeHealthy, "consensus present"},
		{"data plane off entirely", func(o *Options, r *fakeRunner) { o.Dataplane = false; r.rc[pingBin] = 1 },
			CodeHealthy, "consensus present"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := okRunner()
			o := simOptions(r, &fakeSystemd{})
			c.tune(&o, r)
			var out bytes.Buffer
			if rc := Verify(&out, o); rc != c.want {
				t.Fatalf("exit = %d, want %d:\n%s", rc, c.want, out.String())
			}
			if !strings.Contains(out.String(), c.log) {
				t.Errorf("missing %q in:\n%s", c.log, out.String())
			}
		})
	}
}

func TestVerifyConsensusOnly(t *testing.T) {
	r := okRunner()
	s := &fakeSystemd{
		available: true,
		states:    map[string]UnitState{"pq-katzenpost-mixserver": {Unit: "pq-katzenpost-mixserver", Failed: true}},
	}
	o := simOptions(r, s)
	o.Generic = false
	o.ConsensusOnly = true
	o.Dataplane = true
	r.rc[pingBin] = 1

	var out bytes.Buffer
	if rc := Verify(&out, o); rc != CodeHealthy {
		t.Fatalf("exit = %d, want 0:\n%s", rc, out.String())
	}
	if len(s.asked) != 0 {
		t.Errorf("consensus-only consulted systemd for %v", s.asked)
	}
	if r.ran(pingBin) != nil {
		t.Error("consensus-only ran the data plane")
	}
}

func TestVerifyCrashCheck(t *testing.T) {
	unit := "pq-katzenpost-mixserver"
	cases := []struct {
		name  string
		state UnitState
		want  int
		fetch bool
	}{
		{"healthy unit proceeds to the consensus check",
			UnitState{unit, false, "active", 0, true}, CodeHealthy, true},
		{"failed unit rolls back",
			UnitState{unit, true, "active", 0, true}, CodeFailed, false},
		{"transient unit retries",
			UnitState{unit, false, "activating", 0, true}, CodeNotYet, false},
		{"crash-looping unit rolls back",
			UnitState{unit, false, "active", 9, true}, CodeFailed, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := okRunner()
			s := &fakeSystemd{available: true, states: map[string]UnitState{unit: c.state}}
			o := simOptions(r, s)
			o.Generic = false
			o.Dataplane = false
			var out bytes.Buffer
			if rc := Verify(&out, o); rc != c.want {
				t.Fatalf("exit = %d, want %d:\n%s", rc, c.want, out.String())
			}
			if got := r.ran(fetchBin) != nil; got != c.fetch {
				t.Errorf("fetch run = %v, want %v", got, c.fetch)
			}
			if len(s.asked) != 1 || s.asked[0] != unit {
				t.Errorf("systemd asked about %v, want [%s]", s.asked, unit)
			}
		})
	}
}

func TestVerifyWithoutSystemd(t *testing.T) {
	r := okRunner()
	o := simOptions(r, &fakeSystemd{available: false})
	o.Generic = false
	o.Dataplane = false
	o.Units = func() ([]string, error) { return nil, errNoServices() }
	var out bytes.Buffer
	if rc := Verify(&out, o); rc != CodeHealthy {
		t.Fatalf("exit = %d, want 0:\n%s", rc, out.String())
	}
}

func errNoServices() error {
	_, err := units(track.Env{}, "")
	return err
}

func TestVerifyNoServices(t *testing.T) {
	r := okRunner()
	o := simOptions(r, &fakeSystemd{available: true})
	o.Generic = false
	o.Units = func() ([]string, error) { return nil, errNoServices() }
	var out bytes.Buffer
	if rc := Verify(&out, o); rc != CodeNotYet {
		t.Fatalf("exit = %d, want 1:\n%s", rc, out.String())
	}
	if !strings.Contains(out.String(), "This host runs no services") {
		t.Errorf("missing the service_list message in:\n%s", out.String())
	}
}

func realThinclient(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "thinclient.toml")
	if err := os.WriteFile(p, []byte("[Dial]\n  [Dial.Unix]\n    Address = \"@katzenpost\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCannotCheckConsensusIsNotNoConsensus(t *testing.T) {
	const refused = "fetch: dial unix @katzenpost: connect: connection refused"
	cases := []struct {
		name     string
		rc       int
		out      string
		noThin   bool
		wantCode int
		want     string
		notWant  string
	}{
		{
			name: "the network really has no consensus", rc: 1, out: "Error: no document",
			wantCode: CodeNoConsensus,
			want:     "no consensus (fetch exit 1)", notWant: "cannot check consensus",
		},
		{
			name: "kpclientd is down", rc: 1, out: refused,
			wantCode: CodeCannotAsk,
			want:     "cannot ask about consensus: ", notWant: "no local cause",
		},
		{
			name: "no thin-client config on this host", rc: 1, out: refused, noThin: true,
			wantCode: CodeCannotAsk,
			want:     "cannot check consensus: ",
			notWant:  "no local cause",
		},
		{
			name: "fetch timed out", rc: 124, out: refused, wantCode: CodeCannotAsk,
			want: "cannot check consensus: ", notWant: "no local cause",
		},
		{
			name: "fetch will not execute", rc: 126, out: refused, wantCode: CodeCannotAsk,
			want: "cannot check consensus: ", notWant: "no local cause",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := okRunner()
			r.rc[fetchBin] = c.rc
			r.out[fetchBin] = c.out
			o := simOptions(r, &fakeSystemd{})
			o.Dataplane = false
			o.Thinclient = realThinclient(t)
			if c.noThin {
				o.Thinclient = filepath.Join(t.TempDir(), "absent.toml")
			}
			var out bytes.Buffer
			rc := Verify(&out, o)
			if rc != c.wantCode {
				t.Fatalf("exit = %d, want %d (%s):\n%s",
					rc, c.wantCode, Verdict(c.wantCode), out.String())
			}

			if rc == CodeFailed || rc == 127 {
				t.Fatalf("exit %d would roll a release back on no evidence, or"+
					" refuse to repair a node that is down", rc)
			}
			got := out.String()
			if !strings.Contains(got, c.want) {
				t.Errorf("missing %q in:\n%s", c.want, got)
			}
			if strings.Contains(got, c.notWant) {
				t.Errorf("still says %q, which is the wrong diagnosis:\n%s", c.notWant, got)
			}

			if !strings.Contains(got, c.out) {
				t.Errorf("the fetch tool's own output was discarded:\n%s", got)
			}
		})
	}
}

func TestABrokenFetchStillFallsBackToTheJournal(t *testing.T) {
	r := okRunner()
	r.rc[fetchBin] = 1
	r.out[journalBin] = "Jan 01 00:00:00 h kpclientd[1]: fetched new consensus for epoch 5"
	o := simOptions(r, &fakeSystemd{})
	o.Dataplane = false
	o.Thinclient = filepath.Join(t.TempDir(), "absent.toml")
	o.Journalctl = journalBin
	o.ConsensusPattern = defaultConsensusPattern
	o.Since = defaultSince

	var out bytes.Buffer
	if rc := Verify(&out, o); rc != CodeHealthy {
		t.Fatalf("exit = %d, want %d -- a host whose fetch path is broken but whose"+
			" client has consensus must not be refused an upgrade:\n%s",
			rc, CodeHealthy, out.String())
	}
	for _, want := range []string{"cannot check consensus", "kpclientd logged a consensus fetch"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}

	r2 := okRunner()
	r2.rc[fetchBin] = 1
	r2.out[journalBin] = "Jan 01 00:00:00 h kpclientd[1]: dialing"
	o.Run = r2
	var out2 bytes.Buffer
	if rc := Verify(&out2, o); rc != CodeCannotAsk {
		t.Fatalf("exit = %d, want %d -- the host could not ask, which is not the"+
			" same as the network having no answer:\n%s", rc, CodeCannotAsk, out2.String())
	}
	if !strings.Contains(out2.String(), "kno could not ask for a consensus") {
		t.Errorf("missing the honest closing line in:\n%s", out2.String())
	}
	if strings.Contains(out2.String(), "no local cause") {
		t.Errorf("turned 'could not ask' back into a claim about the network:\n%s", out2.String())
	}
}

func TestExecRunnerKeepsStderrOnFailureAndOutOfTheDumpOnSuccess(t *testing.T) {
	out, rc := ExecRunner{}.Run(0, "sh", "-c", "echo to-stdout; echo to-stderr >&2; exit 3")
	if rc != 3 {
		t.Fatalf("rc = %d, want 3", rc)
	}
	for _, want := range []string{"to-stdout", "to-stderr"} {
		if !strings.Contains(out, want) {
			t.Errorf("a failing tool's %q was dropped: %q", want, out)
		}
	}
	out, rc = ExecRunner{}.Run(0, "sh", "-c", "echo wauland; echo noise >&2")
	if rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	if strings.Contains(out, "noise") {
		t.Errorf("stderr leaked into the consensus dump: %q", out)
	}
}

func TestVerifySaysWhatItsExitStatusMeans(t *testing.T) {
	for _, c := range []struct {
		stub string
		code int
		want string
	}{
		{"ok", CodeHealthy, "verified (exit 0)"},
		{"notyet", CodeNotYet, "not yet (exit 1)"},
		{"fail", CodeFailed, "FAILED (exit 2)"},
	} {
		t.Run(c.stub, func(t *testing.T) {
			var out bytes.Buffer
			if rc := Verify(&out, Options{Stub: c.stub}); rc != c.code {
				t.Fatalf("exit = %d, want %d", rc, c.code)
			}
			if !strings.Contains(out.String(), c.want) {
				t.Errorf("missing %q in:\n%s", c.want, out.String())
			}
		})
	}

	var out bytes.Buffer
	o := simOptions(okRunner(), &fakeSystemd{
		available: true,
		states:    map[string]UnitState{"u": {Unit: "u", Active: "activating", Restarts: -1}},
	})
	o.Generic, o.Dataplane = false, false
	o.Units = func() ([]string, error) { return []string{"u"}, nil }
	if rc := Verify(&out, o); rc != CodeNotYet {
		t.Fatalf("exit = %d, want %d:\n%s", rc, CodeNotYet, out.String())
	}
	for _, want := range []string{"u is activating (transient)", "Ask again later", "kno update rolls back"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
}

func TestVerifyConsensusOutcomes(t *testing.T) {
	cases := []struct {
		name string
		tune func(*Options, *fakeRunner)
		want int
		log  string
	}{
		{"fetch reports no consensus", func(o *Options, r *fakeRunner) {
			o.Thinclient = realThinclient(t)
			r.rc[fetchBin] = 1
		}, CodeNoConsensus, "no consensus (fetch exit 1)"},
		{"generic mode needs a fetch tool", func(o *Options, r *fakeRunner) { o.Fetch = "" },
			CodeNotYet, "generic verify needs a fetch tool (set KNO_FETCH)"},
		{"no fetch tool and no journal", func(o *Options, r *fakeRunner) { o.Generic = false; o.Fetch = "" },
			CodeCannotAsk, "kno cannot find kpfetch or pq-katzenpost-fetch. kno reads the kpclientd log instead."},
		{"min replicas adds the readiness flags", func(o *Options, r *fakeRunner) { o.MinReplicas = "2" },
			CodeHealthy, "consensus present"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := okRunner()
			o := simOptions(r, &fakeSystemd{})
			o.Dataplane = false
			c.tune(&o, r)
			var out bytes.Buffer
			if rc := Verify(&out, o); rc != c.want {
				t.Fatalf("exit = %d, want %d:\n%s", rc, c.want, out.String())
			}
			if !strings.Contains(out.String(), c.log) {
				t.Errorf("missing %q in:\n%s", c.log, out.String())
			}
		})
	}

	r := okRunner()
	o := simOptions(r, &fakeSystemd{})
	o.Dataplane = false
	o.MinReplicas = "2"
	Verify(&bytes.Buffer{}, o)
	want := []string{"-f", "/sim/thinclient.toml", "-l", "ERROR", "--format", "json", "--require-ready", "-r", "2", "--ready-timeout", "2m"}
	if c := r.ran(fetchBin); c == nil || !reflect.DeepEqual(c.args, want) {
		t.Errorf("fetch argv = %+v, want %v", c, want)
	}
}

func TestVerifyJournalFallback(t *testing.T) {
	for _, c := range []struct {
		name    string
		journal string
		want    int
		log     string
	}{
		{"journal shows a consensus", "Jan 01 00:00:00 h kpclientd[1]: fetched new consensus for epoch 5",
			CodeHealthy, "kpclientd logged a consensus fetch (journalctl --since -10min)"},

		{"journal shows nothing", "Jan 01 00:00:00 h kpclientd[1]: dialing",
			CodeCannotAsk, "kno could not ask for a consensus"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := okRunner()
			r.missing = map[string]bool{fetchBin: true}
			r.out[journalBin] = c.journal
			o := simOptions(r, &fakeSystemd{})
			o.Dataplane = false
			o.Journalctl = journalBin
			o.ConsensusPattern = defaultConsensusPattern
			o.Since = defaultSince
			var out bytes.Buffer
			if rc := Verify(&out, o); rc != c.want {
				t.Fatalf("exit = %d, want %d:\n%s", rc, c.want, out.String())
			}
			if !strings.Contains(out.String(), c.log) {
				t.Errorf("missing %q in:\n%s", c.log, out.String())
			}
			jc := r.ran(journalBin)
			if jc == nil {
				t.Fatalf("journalctl was never run; calls=%+v", r.calls)
			}
			if !reflect.DeepEqual(jc.args, []string{"-u", "kpclientd", "--since", "-10min"}) {
				t.Errorf("journalctl argv = %v", jc.args)
			}
			if !strings.Contains(out.String(), "kno cannot run /simbin/fetch (exit 127)") {
				t.Errorf("missing the rc=127 warning in:\n%s", out.String())
			}
		})
	}
}

func TestVerifyParticipation(t *testing.T) {
	cases := []struct {
		name string
		tune func(*Options)
		want int
		log  string
	}{
		{"node present", func(o *Options) {
			o.CheckParticipation = true
			o.Identifiers = func() []NodeIdentifier { return []NodeIdentifier{{"node", "wauland"}} }
		}, CodeHealthy, "consensus present, node participating"},
		{"node excluded", func(o *Options) {
			o.CheckParticipation = true
			o.Identifiers = func() []NodeIdentifier { return []NodeIdentifier{{"node", "windfall"}} }
		}, CodeNotYet, "The consensus does not list node 'windfall' yet."},
		{"no identifiers resolve", func(o *Options) {
			o.CheckParticipation = true
			o.Identifiers = func() []NodeIdentifier { return nil }
		}, CodeNotYet, "kno cannot check participation"},
		{"off when not asked for", func(o *Options) {
			o.Identifiers = func() []NodeIdentifier { panic("identifiers must not be resolved") }
		}, CodeHealthy, "consensus present"},
		{"generic skips it", func(o *Options) {
			o.CheckParticipation = true
			o.Generic = true
			o.Identifiers = func() []NodeIdentifier { panic("identifiers must not be resolved") }
		}, CodeHealthy, "consensus present"},
		{"consensus-only skips it", func(o *Options) {
			o.CheckParticipation = true
			o.ConsensusOnly = true
			o.Identifiers = func() []NodeIdentifier { panic("identifiers must not be resolved") }
		}, CodeHealthy, "consensus present"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := simOptions(okRunner(), &fakeSystemd{})
			o.Generic = false
			o.Dataplane = false
			o.Systemd = &fakeSystemd{available: false}
			c.tune(&o)
			var out bytes.Buffer
			if rc := Verify(&out, o); rc != c.want {
				t.Fatalf("exit = %d, want %d:\n%s", rc, c.want, out.String())
			}
			if !strings.Contains(out.String(), c.log) {
				t.Errorf("missing %q in:\n%s", c.log, out.String())
			}
		})
	}

	o := simOptions(okRunner(), &fakeSystemd{})
	o.Dataplane = false
	o.CheckParticipation = true
	o.Identifiers = func() []NodeIdentifier { panic("generic mode must not resolve identifiers") }
	var out bytes.Buffer
	if rc := Verify(&out, o); rc != CodeHealthy {
		t.Fatalf("generic participation: exit = %d, want 0:\n%s", rc, out.String())
	}
}

func TestVerifyStub(t *testing.T) {
	for stub, want := range map[string]int{"ok": CodeHealthy, "notyet": CodeNotYet, "fail": CodeFailed} {
		o := simOptions(okRunner(), &fakeSystemd{available: true})
		o.Stub = stub
		if rc := Verify(&bytes.Buffer{}, o); rc != want {
			t.Errorf("stub %q: exit = %d, want %d", stub, rc, want)
		}
	}
}

func TestMatchPattern(t *testing.T) {
	for _, c := range []struct {
		pattern, out string
		want         bool
	}{
		{defaultConsensusPattern, "kpclientd: Fetched a new consensus", true},
		{defaultConsensusPattern, "fetched the consensus document", true},
		{defaultConsensusPattern, "no consensus", false},
		{"[", "anything", false},
	} {
		if got := matchPattern(c.pattern, c.out); got != c.want {
			t.Errorf("matchPattern(%q, %q) = %v, want %v", c.pattern, c.out, got, c.want)
		}
	}
}

func TestManifestUnits(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "manifest")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a-pq-mixserver.conf", "BACKEND=\"systemd\"\nUNIT=\"pq-katzenpost-mixserver\"\nBUILD=\"server\"\n")
	write("a-pq-storagenode-0.conf", "UNIT=\"pq-katzenpost-storage-replica-0\"\n")
	write("blank.conf", "BACKEND=\"systemd\"\nUNIT=\"\"\n")
	write("ignored.txt", "UNIT=\"nope\"\n")
	write("respectmy-pq-authority.conf", "BACKEND=\"freebsd-ezjail\"\nUNIT=\"katzenpost\"\nBUILD=\"dirauth\"\nJAIL=\"respectmy\"\n")

	got := manifestUnits(dir)
	want := []string{"pq-katzenpost-mixserver", "pq-katzenpost-storage-replica-0", "kpauthority/katzenpost"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("manifestUnits = %v, want %v", got, want)
	}
}

func TestUnits(t *testing.T) {
	repo := testutil.RepoRoot(t)
	e := track.Env{
		Repo:    repo,
		Network: "namenlos",
		Services: []string{
			"wauland-pq-mixserver",
			"annares-pq-storagenode-0",
			"mixy-pq-mixserver",
			"not-a-service",
		},
	}
	got, err := units(e, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"kp-mix", "kpreplica@annares-pq-storagenode-0", "kpserver@mixy-pq-mixserver"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("units = %v, want %v", got, want)
	}

	if _, err := units(track.Env{Repo: repo, Network: "namenlos"}, ""); err == nil {
		t.Error("a host with no services must report the service_list error")
	}

	rel := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rel, "manifest"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rel, "manifest", "x.conf"), []byte("UNIT=\"staged-unit\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err = units(track.Env{Repo: repo, Network: "namenlos"}, rel)
	if err != nil || !reflect.DeepEqual(got, []string{"staged-unit"}) {
		t.Fatalf("units(release) = %v, %v", got, err)
	}
}

func copyConfigs(t *testing.T, dst string, services ...string) {
	t.Helper()
	src := knopath.ConfigsDir(testutil.RepoRoot(t), "namenlos")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, s := range services {
		b, err := os.ReadFile(filepath.Join(src, s+".toml"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, s+".toml"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNodeIdentifiers(t *testing.T) {
	svcs := []string{"wauland-pq-mixserver", "wauland-pq-authority", "wauland-pq-gatewaynode"}
	want := []NodeIdentifier{
		{"authority", "wauland"},
		{"node", "wauland"},
		{"node", "waulandgateway"},
	}

	t.Run("committed .generated/configs, no staged configs dir", func(t *testing.T) {
		repo := t.TempDir()
		copyConfigs(t, knopath.ConfigsDir(repo, "namenlos"), svcs...)
		e := track.Env{Repo: repo, Network: "namenlos", Services: svcs}
		if got := nodeIdentifiers(e, svcs); !reflect.DeepEqual(got, want) {
			t.Fatalf("nodeIdentifiers = %+v, want %+v", got, want)
		}
	})

	t.Run("staged configs dir only", func(t *testing.T) {
		repo := t.TempDir()
		copyConfigs(t, filepath.Join(repo, "configs"), svcs...)
		e := track.Env{Repo: repo, Network: "namenlos", Services: svcs}
		if got := nodeIdentifiers(e, svcs); !reflect.DeepEqual(got, want) {
			t.Fatalf("nodeIdentifiers = %+v, want %+v", got, want)
		}
	})

	t.Run("nothing to read", func(t *testing.T) {
		e := track.Env{Repo: t.TempDir(), Network: "namenlos", Services: svcs}
		if got := nodeIdentifiers(e, svcs); len(got) != 0 {
			t.Fatalf("nodeIdentifiers = %+v, want none", got)
		}
	})
}

func TestFindTool(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	bad := filepath.Join(dir, "bad")
	if err := os.WriteFile(good, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing")

	if got := FindTool(good, missing); got != good {
		t.Errorf("explicit runnable tool = %q, want %q", got, good)
	}
	if got := FindTool(bad, good); got != "" {
		t.Errorf("explicit non-executable tool = %q, want empty (no fallback)", got)
	}
	if got := FindTool(missing, good); got != "" {
		t.Errorf("explicit missing tool = %q, want empty (no fallback)", got)
	}
	if got := FindTool("", missing, good); got != good {
		t.Errorf("candidate scan = %q, want %q", got, good)
	}
	if got := FindTool("", missing); got != "" {
		t.Errorf("no runnable candidate = %q, want empty", got)
	}
	if got := FindTool("", "sh"); got == "" {
		t.Error("a bare name should resolve through PATH")
	}
}

func writeKnoToml(t *testing.T, repo string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "kno.toml")
	body := `network = "namenlos"
nodes = ["wauland"]
[update]
policy = "apply"
require_signed_tag = false
[push]
mode = "full"
[hosts.testhost]
scheduler = "systemd"
services = ["wauland-pq-mixserver"]
`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOptionsFromEnv(t *testing.T) {
	repo := testutil.RepoRoot(t)
	t.Setenv("KNO_CONFIG", writeKnoToml(t, repo))
	t.Setenv("KNO_REPO", repo)
	t.Setenv("KNO_HOST", "testhost")

	t.Run("defaults", func(t *testing.T) {
		for _, k := range []string{
			"VERIFY_GENERIC", "VERIFY_DATAPLANE", "VERIFY_CONSENSUS_ONLY", "VERIFY_ECHO",
			"VERIFY_PIGEONHOLE", "VERIFY_THINCLIENT", "VERIFY_PING_COUNT", "VERIFY_PING_TIMEOUT",
			"VERIFY_MAX_RESTARTS", "VERIFY_MIN_REPLICAS", "VERIFY_CHECK_PARTICIPATION",
			"VERIFY_NODE_IDENTIFIERS", "KNO_FETCH", "KNO_PING", "KNO_CPBENCH", "KNO_TEST", "VERIFY_STUB",
		} {
			t.Setenv(k, "")
		}
		o, err := OptionsFromEnv(nil)
		if err != nil {
			t.Fatal(err)
		}
		if o.Generic || o.Dataplane || o.ConsensusOnly || o.Pigeonhole {
			t.Errorf("modes default on: %+v", o)
		}
		if !o.CheckParticipation {
			t.Error("participation is not checked by default, but kno update checks it")
		}
		if !o.Echo {
			t.Error("VERIFY_ECHO defaults to 1")
		}
		if o.Thinclient != defaultThinclient(runtime.GOOS == "freebsd") || o.PingCount != "3" || o.MaxRestarts != 3 {
			t.Errorf("defaults = %q %q %d", o.Thinclient, o.PingCount, o.MaxRestarts)
		}
		if o.PingTimeout != 120*time.Second || o.FetchTimeout != 120*time.Second || o.CPBenchTimeout != 300*time.Second {
			t.Errorf("timeouts = %v %v %v", o.PingTimeout, o.FetchTimeout, o.CPBenchTimeout)
		}
		if o.ConsensusPattern != defaultConsensusPattern || o.Since != defaultSince || o.ReadyTimeout != "2m" {
			t.Errorf("journal/ready defaults = %q %q %q", o.ConsensusPattern, o.Since, o.ReadyTimeout)
		}
		if o.Stub != "" {
			t.Errorf("VERIFY_STUB honoured without KNO_TEST=1: %q", o.Stub)
		}
	})

	t.Run("participation off", func(t *testing.T) {
		t.Setenv("VERIFY_CHECK_PARTICIPATION", "0")
		o, err := OptionsFromEnv(nil)
		if err != nil {
			t.Fatal(err)
		}
		if o.CheckParticipation {
			t.Error("VERIFY_CHECK_PARTICIPATION=0 did not turn the check off")
		}
		o, err = OptionsFor(nil, Flags{participationEnv: "1"})
		if err != nil {
			t.Fatal(err)
		}
		if !o.CheckParticipation {
			t.Error("--participation did not beat VERIFY_CHECK_PARTICIPATION=0")
		}
		t.Setenv("VERIFY_CHECK_PARTICIPATION", "")
		o, err = OptionsFor(nil, Flags{participationEnv: "0"})
		if err != nil {
			t.Fatal(err)
		}
		if o.CheckParticipation {
			t.Error("--participation=false did not turn the check off")
		}
		if v, set := os.LookupEnv("VERIFY_CHECK_PARTICIPATION"); !set || v != "" {
			t.Errorf("the flag leaked into the environment: %q %v", v, set)
		}
	})

	t.Run("simulator environment", func(t *testing.T) {
		bin := t.TempDir()
		for _, n := range []string{"fetch", "ping", "cp-bench"} {
			if err := os.WriteFile(filepath.Join(bin, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		t.Setenv("VERIFY_GENERIC", "1")
		t.Setenv("VERIFY_DATAPLANE", "1")
		t.Setenv("VERIFY_THINCLIENT", "/sim/thinclient.toml")
		t.Setenv("VERIFY_PING_TIMEOUT", "600")
		t.Setenv("VERIFY_PING_COUNT", "3")
		t.Setenv("KNO_FETCH", filepath.Join(bin, "fetch"))
		t.Setenv("KNO_PING", filepath.Join(bin, "ping"))
		t.Setenv("KNO_CPBENCH", filepath.Join(bin, "cp-bench"))

		o, err := OptionsFromEnv(nil)
		if err != nil {
			t.Fatal(err)
		}
		if !o.Generic || !o.Dataplane || o.ConsensusOnly {
			t.Errorf("modes = %+v", o)
		}
		if o.Thinclient != "/sim/thinclient.toml" || o.PingTimeout != 600*time.Second || o.PingCount != "3" {
			t.Errorf("sim options = %q %v %q", o.Thinclient, o.PingTimeout, o.PingCount)
		}
		if o.Fetch != filepath.Join(bin, "fetch") || o.Ping != filepath.Join(bin, "ping") || o.CPBench != filepath.Join(bin, "cp-bench") {
			t.Errorf("tools = %q %q %q", o.Fetch, o.Ping, o.CPBench)
		}
	})

	t.Run("explicit node identifiers", func(t *testing.T) {
		t.Setenv("VERIFY_CHECK_PARTICIPATION", "1")
		t.Setenv("VERIFY_NODE_IDENTIFIERS", "wauland waulandgateway")
		o, err := OptionsFromEnv(nil)
		if err != nil {
			t.Fatal(err)
		}
		want := []NodeIdentifier{{"any", "wauland"}, {"any", "waulandgateway"}}
		if got := o.Identifiers(); !reflect.DeepEqual(got, want) {
			t.Errorf("identifiers = %+v, want %+v", got, want)
		}
	})

	t.Run("derived node identifiers", func(t *testing.T) {
		t.Setenv("VERIFY_NODE_IDENTIFIERS", "")
		o, err := OptionsFromEnv(nil)
		if err != nil {
			t.Fatal(err)
		}
		want := []NodeIdentifier{{"node", "wauland"}}
		if got := o.Identifiers(); !reflect.DeepEqual(got, want) {
			t.Errorf("identifiers = %+v, want %+v", got, want)
		}
		us, err := o.Units()
		if err != nil || !reflect.DeepEqual(us, []string{"kp-mix"}) {
			t.Errorf("units = %v, %v", us, err)
		}
	})

	t.Run("test stub", func(t *testing.T) {
		t.Setenv("KNO_TEST", "1")
		t.Setenv("VERIFY_STUB", "fail")
		o, err := OptionsFromEnv(nil)
		if err != nil {
			t.Fatal(err)
		}
		if o.Stub != "fail" {
			t.Errorf("stub = %q", o.Stub)
		}
	})

	t.Run("bad numeric setting", func(t *testing.T) {
		t.Setenv("VERIFY_MAX_RESTARTS", "many")
		if _, err := OptionsFromEnv(nil); err == nil {
			t.Error("a non-numeric VERIFY_MAX_RESTARTS must be rejected")
		}
	})
}

func TestCommandHelp(t *testing.T) {
	var out bytes.Buffer
	c := Command()
	c.SetArgs([]string{"--help"})
	c.SetOut(&out)
	c.SetErr(&out)
	if err := c.Execute(); err != nil {
		t.Fatalf("help failed: %v", err)
	}

	for _, want := range []string{"consensus", "release-dir"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help does not mention %q: %q", want, out.String())
		}
	}
}

func TestAFetchThatCouldNotAskSaysSo(t *testing.T) {
	down := "pq-katzenpost-fetch: Failed to connect to client daemon: " +
		"dial unix @katzenpost: connect: connection refused."
	if got := clientDaemonDown(down); got == "" {
		t.Errorf("a refused socket was not recognised as a local fault: %q", down)
	}

	for _, out := range []string{
		"no consensus for epoch 244289",
		"",
		"error: document not found",
	} {
		if got := clientDaemonDown(out); got != "" {
			t.Errorf("%q was blamed on the local daemon: %s", out, got)
		}
	}
}

func TestFlagsBeatTheVariablesAndDoNotLeak(t *testing.T) {
	t.Setenv("VERIFY_MAX_RESTARTS", "3")
	t.Setenv("VERIFY_FETCH_TIMEOUT", "120")

	o, err := OptionsFor(nil, Flags{
		"VERIFY_CONSENSUS_ONLY": "1",
		participationEnv:        "1",
		"VERIFY_DATAPLANE":      "1",
		"VERIFY_MAX_RESTARTS":   "9",
		"VERIFY_FETCH_TIMEOUT":  "7",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !o.ConsensusOnly || !o.CheckParticipation || !o.Dataplane {
		t.Errorf("a boolean flag did not reach the options: %+v", o)
	}
	if o.MaxRestarts != 9 {
		t.Errorf("MaxRestarts = %d, want the flag's 9", o.MaxRestarts)
	}
	if o.FetchTimeout != 7*time.Second {
		t.Errorf("FetchTimeout = %s, want the flag's 7s", o.FetchTimeout)
	}

	if v := os.Getenv("VERIFY_MAX_RESTARTS"); v != "3" {
		t.Errorf("the flag leaked into the environment: VERIFY_MAX_RESTARTS=%q", v)
	}
	if _, set := os.LookupEnv("VERIFY_CONSENSUS_ONLY"); set {
		t.Error("a boolean flag was left set in the environment")
	}

	t.Setenv("VERIFY_DATAPLANE", "1")
	o, err = OptionsFor(nil, Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if !o.Dataplane || o.MaxRestarts != 3 {
		t.Errorf("the variables stopped working: %+v", o)
	}
}

func clearForwarded(t *testing.T) {
	t.Helper()
	for _, k := range append([]string{participationEnv}, forwarded...) {
		t.Setenv(k, "")
	}
}

func TestVerifyOverHostForwardsItsModes(t *testing.T) {
	clearForwarded(t)
	if got := (Flags{}).remoteCommand(nil); got != "VERIFY_CHECK_PARTICIPATION=1 kno verify" {
		t.Errorf("an older kno on the host does not check participation unless told: %q", got)
	}
	if got := (Flags{participationEnv: "0"}).remoteCommand(nil); got != "VERIFY_CHECK_PARTICIPATION=0 kno verify" {
		t.Errorf("--participation=false did not reach the host: %q", got)
	}
	t.Setenv("VERIFY_CHECK_PARTICIPATION", "0")
	if got := (Flags{}).remoteCommand(nil); got != "VERIFY_CHECK_PARTICIPATION=0 kno verify" {
		t.Errorf("VERIFY_CHECK_PARTICIPATION=0 here did not reach the host: %q", got)
	}
	if got := (Flags{participationEnv: "1"}).remoteCommand(nil); got != "VERIFY_CHECK_PARTICIPATION=1 kno verify" {
		t.Errorf("--participation did not beat the variable: %q", got)
	}
	t.Setenv("VERIFY_CHECK_PARTICIPATION", "")
	t.Setenv("VERIFY_DATAPLANE", "1")
	if got := (Flags{}).remoteCommand(nil); !strings.Contains(got, "VERIFY_DATAPLANE=1 kno verify") {
		t.Errorf("VERIFY_DATAPLANE=1 here did not reach the host: %q", got)
	}
	if got := (Flags{"VERIFY_DATAPLANE": "0"}).remoteCommand(nil); !strings.Contains(got, "VERIFY_DATAPLANE=0 kno verify") {
		t.Errorf("--dataplane=false did not reach the host: %q", got)
	}
}

func TestVerifyOverHostForwardsEveryFlagAndTheReleaseDir(t *testing.T) {
	clearForwarded(t)
	t.Setenv("VERIFY_PING_COUNT", "5")
	got := Flags{
		"VERIFY_MAX_RESTARTS":  "9",
		"VERIFY_SINCE":         "@1700000000",
		"VERIFY_FETCH_TIMEOUT": "30",
		"VERIFY_MIN_REPLICAS":  "2",
		"VERIFY_THINCLIENT":    "/etc/my thin'client.toml",
	}.remoteCommand([]string{"/var/lib/kno/releases/v1 x"})
	for _, want := range []string{
		"VERIFY_MAX_RESTARTS=9 ",
		"VERIFY_SINCE=@1700000000 ",
		"VERIFY_FETCH_TIMEOUT=30 ",
		"VERIFY_MIN_REPLICAS=2 ",
		"VERIFY_PING_COUNT=5 ",
		`VERIFY_THINCLIENT='/etc/my thin'\''client.toml' `,
		"kno verify -- '/var/lib/kno/releases/v1 x'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the host does not get %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "VERIFY_DATAPLANE") {
		t.Errorf("a variable that is not set was sent: %s", got)
	}
}

func TestAFalseFlagTurnsItsVariableOff(t *testing.T) {
	repo := testutil.RepoRoot(t)
	t.Setenv("KNO_CONFIG", writeKnoToml(t, repo))
	t.Setenv("KNO_REPO", repo)
	t.Setenv("KNO_HOST", "testhost")
	t.Setenv("VERIFY_DATAPLANE", "1")
	t.Setenv("VERIFY_GENERIC", "1")

	cmd := Command()
	if err := cmd.ParseFlags([]string{"--dataplane=false", "--generic=false", "--max-restarts", "9"}); err != nil {
		t.Fatal(err)
	}
	f := changedFlags(cmd)
	want := Flags{"VERIFY_DATAPLANE": "0", "VERIFY_GENERIC": "0", "VERIFY_MAX_RESTARTS": "9"}
	if !reflect.DeepEqual(f, want) {
		t.Errorf("flags = %v, want %v", f, want)
	}
	o, err := OptionsFor(nil, f)
	if err != nil {
		t.Fatal(err)
	}
	if o.Dataplane || o.Generic {
		t.Errorf("--dataplane=false or --generic=false did not beat the variable: %+v", o)
	}
	if o.MaxRestarts != 9 {
		t.Errorf("MaxRestarts = %d, want 9", o.MaxRestarts)
	}
}

func TestVerboseNamesOnlyTheChecksThatRun(t *testing.T) {
	sd := &fakeSystemd{available: true}
	for _, c := range []struct {
		name string
		o    Options
		want string
	}{
		{"node", Options{Systemd: sd, CheckParticipation: true, Echo: true}, "units consensus participation"},
		{"generic", Options{Systemd: sd, Generic: true, CheckParticipation: true, Echo: true}, "consensus"},
		{"no systemd", Options{Systemd: &fakeSystemd{}, Echo: true}, "consensus"},
		{"data plane", Options{Systemd: sd, Dataplane: true, Echo: true, Pigeonhole: true}, "units consensus echo pigeonhole"},
		{"consensus only", Options{Systemd: sd, ConsensusOnly: true, Dataplane: true, Echo: true}, "consensus"},
	} {
		if got := Enabled(c.o); got != c.want {
			t.Errorf("%s: checking %q, want %q", c.name, got, c.want)
		}
	}
}

func participationOptions(t *testing.T, r *fakeRunner) Options {
	thin := filepath.Join(t.TempDir(), "thinclient.toml")
	if err := os.WriteFile(thin, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := simOptions(r, &fakeSystemd{})
	o.Generic, o.Dataplane, o.CheckParticipation = false, false, true
	o.Thinclient = thin
	o.Identifiers = func() []NodeIdentifier { return []NodeIdentifier{{Where: "any", ID: "annares"}} }
	o.Journalctl = journalBin
	o.ConsensusPattern = defaultConsensusPattern
	o.Since = defaultSince
	return o
}

func TestTheJournalDoesNotStandInForTheParticipationCheck(t *testing.T) {
	r := okRunner()
	r.rc[fetchBin] = 124
	r.out[journalBin] = "Jan 01 00:00:00 h kpclientd[1]: fetched new consensus for epoch 5"
	o := participationOptions(t, r)

	var out bytes.Buffer
	if rc := Verify(&out, o); rc != CodeCannotAsk {
		t.Fatalf("exit = %d, want %d: a consensus in the journal does not show"+
			" that this node is in it:\n%s", rc, CodeCannotAsk, out.String())
	}
	if !strings.Contains(out.String(), "participation is NOT checked") {
		t.Errorf("verify does not say that participation was not checked:\n%s", out.String())
	}

	o.CheckParticipation = false
	out.Reset()
	if rc := Verify(&out, o); rc != CodeHealthy {
		t.Errorf("without the participation check the journal is enough; exit = %d:\n%s", rc, out.String())
	}

	o.CheckParticipation, o.ConsensusOnly = true, true
	out.Reset()
	if rc := Verify(&out, o); rc != CodeHealthy {
		t.Errorf("a consensus-only check accepts the journal; exit = %d:\n%s", rc, out.String())
	}
}

func TestACrashedFetchIsAFaultOnThisHost(t *testing.T) {
	for _, c := range []struct {
		name string
		rc   int
		out  string
		want int
	}{
		{"signal", -1, "", CodeCannotAsk},
		{"illegal instruction", 2, "SIGILL: illegal instruction\nPC=0x10000", CodeCannotAsk},
		{"go panic", 2, "panic: runtime error: index out of range", CodeCannotAsk},
		{"an error", 1, "Error: no document", CodeNoConsensus},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := okRunner()
			r.rc[fetchBin] = c.rc
			r.out[fetchBin] = c.out
			r.out[journalBin] = "Jan 01 00:00:00 h kpclientd[1]: dialing"
			o := participationOptions(t, r)
			var out bytes.Buffer
			if rc := Verify(&out, o); rc != c.want {
				t.Fatalf("exit = %d, want %d:\n%s", rc, c.want, out.String())
			}
			if c.want == CodeCannotAsk && strings.Contains(out.String(), "no local cause") {
				t.Errorf("a crash on this host was reported as the network's:\n%s", out.String())
			}
		})
	}
}
