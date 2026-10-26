package doctor

import (
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/sshx"
	"github.com/katzenpost/kno/internal/testutil"
	"github.com/katzenpost/kno/internal/track"
)

func envWith(hosts map[string]config.Host) track.Env {
	return track.Env{ConfigPath: "/etc/katzenpost/kno.toml", Config: config.Config{Hosts: hosts}}
}

func TestHostLabelResolvesToItsSSHTarget(t *testing.T) {
	e := envWith(map[string]config.Host{
		"mixy": {SSH: "root@mixy"},
		"reed": {SSH: "root@reed"},
	})
	got, err := nodeFor(e, "mixy")
	if err != nil || got != "root@mixy" {
		t.Fatalf("nodeFor = %q %v", got, err)
	}
}

func TestAnUnknownHostNamesTheOnesThereAre(t *testing.T) {
	e := envWith(map[string]config.Host{"mixy": {SSH: "root@mixy"}, "reed": {SSH: "root@reed"}})
	_, err := nodeFor(e, "mixxy")
	if err == nil {
		t.Fatal("an unknown host was accepted")
	}
	for _, want := range []string{"mixxy", "mixy", "reed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
}

func TestAHostWithNoSSHTargetSaysWhichItIs(t *testing.T) {
	e := envWith(map[string]config.Host{"local": {}})
	_, err := nodeFor(e, "local")
	if err == nil {
		t.Fatal("a host with no ssh target was accepted")
	}
	msg := err.Error()
	if !strings.Contains(msg, "without --host") && !strings.Contains(msg, "no ssh target") {
		t.Errorf("the error says neither what to run nor what is missing: %v", err)
	}
}

func TestNoHostsAtAllPointsAtSetup(t *testing.T) {
	_, err := nodeFor(track.Env{}, "mixy")
	if err == nil || !strings.Contains(err.Error(), "kno setup") {
		t.Fatalf("err = %v, want it to point at kno setup", err)
	}
}

func TestTheFlagsReachTheFarSide(t *testing.T) {
	for _, c := range []struct {
		fix, dry, verbose bool
		want              string
	}{
		{want: "kno doctor"},
		{verbose: true, want: "kno doctor --verbose"},
		{fix: true, want: "kno doctor --fix"},
		{fix: true, dry: true, want: "kno doctor --fix --dry-run"},
		{fix: true, dry: true, verbose: true, want: "kno doctor --verbose --fix --dry-run"},

		{dry: true, want: "kno doctor"},
	} {
		if got := remoteDoctorCmdFor("", c.fix, c.dry, c.verbose); got != sshx.AsRoot(c.want) {
			t.Errorf("fix=%v dry=%v verbose=%v -> %q, want %q", c.fix, c.dry, c.verbose, got, sshx.AsRoot(c.want))
		}
	}
}

func TestTheRemoteDoctorRunsAsRoot(t *testing.T) {
	for _, tc := range []struct {
		uid  int
		want string
	}{
		{0, "kno doctor --fix\n"},
		{1000, "sudo LC_ALL=C KNO_DOCTOR_LABEL=mixy kno doctor --fix\n"},
	} {
		script := sshx.CLocale(remoteDoctorCmdFor("mixy", true, false, false))
		out, err := testutil.RemoteShell(t, tc.uid, script, "kno")
		if err != nil || out != tc.want {
			t.Errorf("uid %d: %s\n  printed %q (%v), want %q", tc.uid, script, out, err, tc.want)
		}
	}
}

func TestRemoteDoctorCmdCarriesEveryFlag(t *testing.T) {
	for _, c := range []struct {
		label            string
		fix, dry, verb   bool
		want, mustNotSay string
	}{
		{"annares", true, true, false,
			"KNO_DOCTOR_LABEL=annares kno doctor --fix --dry-run", ""},
		{"annares", true, false, false,
			"KNO_DOCTOR_LABEL=annares kno doctor --fix", "--dry-run"},
		{"annares", false, false, false,
			"KNO_DOCTOR_LABEL=annares kno doctor", "--fix"},

		{"annares", false, true, false,
			"KNO_DOCTOR_LABEL=annares kno doctor", "--dry-run"},
		{"mixy", true, true, true,
			"KNO_DOCTOR_LABEL=mixy kno doctor --verbose --fix --dry-run", ""},
	} {
		got := remoteDoctorCmdFor(c.label, c.fix, c.dry, c.verb)
		if got != sshx.AsRoot(c.want) {
			t.Errorf("got %q, want %q", got, sshx.AsRoot(c.want))
		}
		if c.mustNotSay != "" && strings.Contains(got, c.mustNotSay) {
			t.Errorf("%q must not contain %q", got, c.mustNotSay)
		}
	}
}

func TestHostSectionNamesTheRemote(t *testing.T) {
	t.Setenv("KNO_DOCTOR_LABEL", "annares")
	if got := hostSectionTitle(); got != "annares (reached over ssh)" {
		t.Errorf("got %q, want the host named", got)
	}
	t.Setenv("KNO_DOCTOR_LABEL", "")
	if got := hostSectionTitle(); got != "this host" {
		t.Errorf("run locally it is still this host, got %q", got)
	}
}

func TestAHostWithNoSSHIsReachedAtItsLabel(t *testing.T) {
	t.Setenv("KNO_HOST", "")
	e := envWith(map[string]config.Host{"mixy": {}, "reed": {SSH: "root@reed"}})
	if got, err := nodeFor(e, "mixy"); err != nil || got != "mixy" {
		t.Fatalf("nodeFor = %q %v, want the label, as kno update and kno push use", got, err)
	}
	t.Setenv("KNO_HOST", "mixy")
	if _, err := nodeFor(e, "mixy"); err == nil || !strings.Contains(err.Error(), "is this host") {
		t.Errorf("err = %v, want this host to be refused", err)
	}
}
