package schedule

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/sshx"
	"github.com/katzenpost/kno/internal/testutil"
	"github.com/katzenpost/kno/internal/track"
)

func opts(t *testing.T, mode, host string) Options {
	t.Helper()
	return Options{
		Repo: testutil.RepoRoot(t),
		Mode: mode,
		Host: host,
		SSH:  "ssh",
		User: "kno",
	}
}

func sysd(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(testutil.RepoRoot(t), "systemd", name)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("the unit template the plan installs is missing: %v", err)
	}
	return p
}

func plan(t *testing.T, o Options) Plan {
	t.Helper()
	p, err := PlanFor(o)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func assertCommands(t *testing.T, p Plan, want []string) {
	t.Helper()
	got := p.Commands()
	if len(got) != len(want) {
		t.Fatalf("plan has %d steps, want %d:\n  got  %q\n  want %q", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("step %d:\n  got  %s\n  want %s", i, got[i], want[i])
		}
	}
}

func TestLocalTimer(t *testing.T) {
	p := plan(t, opts(t, ModeTimer, ""))
	assertCommands(t, p, []string{
		"tee /etc/systemd/system/kno-update.service",
		"chmod 0644 /etc/systemd/system/kno-update.service",
		"install -m 0644 " + sysd(t, "kno-update.timer") + " /etc/systemd/system/kno-update.timer",
		"systemctl daemon-reload",
		"systemctl enable --now kno-update.timer",
		"rm -f /etc/cron.d/kno-update",
	})
	if !p.Steps[5].BestEffort {
		t.Error("the removal of the cron entry must not fail an installed timer")
	}
	if !p.Local() {
		t.Error("a plan with no host is local")
	}
}

func TestLocalCron(t *testing.T) {
	p := plan(t, opts(t, ModeCron, ""))
	assertCommands(t, p, []string{
		"tee /etc/cron.d/kno-update",
		"systemctl disable --now kno-update.timer",
		"rm -f /etc/systemd/system/kno-update.timer",
		"systemctl daemon-reload",
	})
	for _, st := range p.Steps[1:] {
		if !st.BestEffort {
			t.Errorf("the removal of the timer must not fail a written cron entry: %q", st.Argv)
		}
	}

	argv := p.Steps[0].Argv
	if want := []string{"tee", "/etc/cron.d/kno-update"}; !reflect.DeepEqual(argv, want) {
		t.Fatalf("cron argv: %q, want %q", argv, want)
	}
	want := "# kno-update periodic check\n" +
		"*/30 * * * * kno /usr/bin/kno update >/dev/null 2>&1\n"
	if p.Steps[0].Stdin != want {
		t.Errorf("cron fragment:\n  got  %q\n  want %q", p.Steps[0].Stdin, want)
	}
}

func TestRemoteValuesAreRefusedNotQuoted(t *testing.T) {
	for _, c := range []struct{ name, host, user, want string }{
		{"host as an ssh option", "-oProxyCommand=id", "kno", "ssh target"},
		{"host with a separator", "node;id", "kno", "ssh target"},
		{"cron user with a separator", "node.example", "kno;id", "updater user"},
		{"cron user with a space", "node.example", "kno root", "updater user"},
	} {
		t.Run(c.name, func(t *testing.T) {
			o := opts(t, ModeCron, c.host)
			o.User = c.user
			if _, err := PlanFor(o); err == nil {
				t.Fatalf("planned a schedule change with a %s", c.name)
			} else if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error does not name the %s: %v", c.want, err)
			}
		})
	}
}

func TestLocalCronUsesNoShell(t *testing.T) {
	for _, mode := range []string{ModeTimer, ModeCron, ModeNone} {
		for _, s := range plan(t, opts(t, mode, "")).Steps {
			for _, a := range s.Argv {
				if a == "sh" || a == "-c" || a == "bash" {
					t.Errorf("%s runs a shell: %q", mode, s.Argv)
				}
			}
		}
	}
}

func TestCronUserIsHonoured(t *testing.T) {
	o := opts(t, ModeCron, "")
	o.User = "katzen"
	if got := CronLine(o.User); !strings.Contains(got, " katzen /usr/bin/kno update") {
		t.Errorf("cron line: %s", got)
	}
	if got := plan(t, o).Steps[0].Stdin; !strings.Contains(got, "* katzen /usr/bin/kno update") {
		t.Errorf("cron fragment ignored KNO_USER: %s", got)
	}
}

func TestNoneInstallsNothing(t *testing.T) {
	for _, host := range []string{""} {
		p := plan(t, opts(t, ModeNone, host))
		joined := strings.Join(p.Commands(), "\n")
		for _, forbidden := range []string{"install -m", "enable", "tee ", "systemctl start"} {
			if strings.Contains(joined, forbidden) {
				t.Errorf("scheduler none ran %q (host=%q):\n%s", forbidden, host, joined)
			}
		}
		if !strings.Contains(joined, "rm -f") {
			t.Errorf("scheduler none removed nothing (host=%q):\n%s", host, joined)
		}
		for _, unit := range []string{"kno-update.timer", "kno-update.service", "/etc/cron.d/kno-update"} {
			if !strings.Contains(joined, unit) {
				t.Errorf("scheduler none left %s behind (host=%q):\n%s", unit, host, joined)
			}
		}
	}
}

func TestLocalNone(t *testing.T) {
	p := plan(t, opts(t, ModeNone, ""))
	assertCommands(t, p, []string{
		"systemctl disable --now kno-update.timer",
		"rm -f /etc/systemd/system/kno-update.timer /etc/systemd/system/kno-update.service /etc/cron.d/kno-update",
		"systemctl daemon-reload",
	})

	if !p.Steps[0].BestEffort || !p.Steps[2].BestEffort {
		t.Error("the systemctl steps are best effort in the shell")
	}
	if p.Steps[1].BestEffort {
		t.Error("the removal must not be best effort")
	}
}

func TestARemoteScheduleRunsOnlyKnoActivateAsRoot(t *testing.T) {
	for _, mode := range []string{ModeTimer, ModeCron, ModeNone} {
		p := plan(t, opts(t, mode, "luscus.example"))
		assertCommands(t, p, []string{"ssh luscus.example " + sshx.AsRoot("kno activate --schedule="+mode)})
		if p.Local() {
			t.Error("a plan with a host is not local")
		}
	}
}

func TestAUserScheduleRunsOnlyKnoActivateThroughSudo(t *testing.T) {
	o := opts(t, ModeTimer, "")
	o.Sudo = "sudo"
	p := plan(t, o)
	if len(p.Steps) != 1 {
		t.Fatalf("plan has %d steps, want 1: %q", len(p.Steps), p.Commands())
	}
	a := p.Steps[0].Argv
	if a[0] != "sudo" || a[2] != "activate" || a[3] != "--schedule=timer" {
		t.Errorf("a user schedule runs more than kno activate as root: %q", a)
	}
}

func TestEmptySudoDropsTheWord(t *testing.T) {
	o := opts(t, ModeTimer, "")
	o.Sudo = ""
	p := plan(t, o)
	for _, s := range p.Steps {
		if s.Argv[0] == "sudo" {
			t.Errorf("sudo survived an empty SUDO: %q", s.Argv)
		}
		for _, a := range s.Argv {
			if a == "" {
				t.Errorf("an empty argument was passed to exec: %q", s.Argv)
			}
		}
	}
	assertCommands(t, p, []string{
		"tee /etc/systemd/system/kno-update.service",
		"chmod 0644 /etc/systemd/system/kno-update.service",
		"install -m 0644 " + sysd(t, "kno-update.timer") + " /etc/systemd/system/kno-update.timer",
		"systemctl daemon-reload",
		"systemctl enable --now kno-update.timer",
		"rm -f /etc/cron.d/kno-update",
	})
}

func TestTransportsAreCommandLines(t *testing.T) {
	o := opts(t, ModeNone, "luscus.example")
	o.SSH = "ssh -i /keys/id_ed25519"
	p := plan(t, o)
	argv := p.Steps[0].Argv
	if len(argv) != 5 || argv[0] != "ssh" || argv[1] != "-i" || argv[2] != "/keys/id_ed25519" || argv[3] != "luscus.example" {
		t.Fatalf("ssh options were not split: %q", argv)
	}

	o = opts(t, ModeTimer, "")
	o.Sudo = "doas -n"
	if got := plan(t, o).Steps[0].Argv[:2]; got[0] != "doas" || got[1] != "-n" {
		t.Errorf("sudo options were not split: %q", got)
	}
}

func TestUnknownModeIsAnError(t *testing.T) {
	for _, host := range []string{"", "luscus.example"} {
		o := opts(t, "hourly", host)
		if _, err := PlanFor(o); err == nil || !strings.Contains(err.Error(), "timer|cron|none") {
			t.Errorf("host=%q: expected an unknown-mode error, got %v", host, err)
		}
	}
}

func TestModeForDefaultsToNone(t *testing.T) {
	if got := ModeFor(config.Host{}); got != ModeNone {
		t.Errorf("a host that says nothing gets %q, want %q", got, ModeNone)
	}
	if got := ModeFor(config.Host{Scheduler: "timer"}); got != ModeTimer {
		t.Errorf("ModeFor = %q", got)
	}
}

func TestOptionsResolveModeFromConfig(t *testing.T) {
	clearEnv(t)
	c := config.Config{
		Network: "namenlos",
		Hosts: map[string]config.Host{
			"annares": {Hostname: "annares", Scheduler: "timer"},
			"luscus":  {SSH: "luscus.example", Scheduler: "cron"},
			"quiet":   {SSH: "quiet.example"},
		},
	}
	e := track.Env{Repo: testutil.RepoRoot(t), Config: c}

	cases := []struct {
		name       string
		args       []string
		hostFlag   string
		wantMode   string
		wantHost   string
		knoHostEnv string
	}{
		{name: "explicit mode wins", args: []string{"cron", "annares"}, wantMode: ModeCron, wantHost: "annares"},
		{name: "label carries its scheduler and ssh", hostFlag: "luscus", wantMode: ModeCron, wantHost: "luscus.example"},
		{name: "label with no scheduler gets none", hostFlag: "quiet", wantMode: ModeNone, wantHost: "quiet.example"},

		{name: "a label with no ssh is still that host", hostFlag: "annares", wantMode: ModeTimer, wantHost: "annares"},

		{name: "the local label is local", hostFlag: "annares", knoHostEnv: "annares", wantMode: ModeTimer, wantHost: ""},

		{name: "the local label with an ssh address is still local", hostFlag: "luscus", knoHostEnv: "luscus", wantMode: ModeCron, wantHost: ""},
		{name: "unknown label is a raw ssh target", hostFlag: "box.example", wantMode: ModeNone, wantHost: "box.example"},
		{name: "no args uses this host", knoHostEnv: "annares", wantMode: ModeTimer, wantHost: ""},

		{name: "no mode for this host names it", args: []string{"none"}, knoHostEnv: "elsewhere", wantMode: ModeNone, wantHost: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("KNO_HOST", tc.knoHostEnv)
			o, err := options(e, tc.args, tc.hostFlag, false)
			if err != nil {
				t.Fatal(err)
			}
			if o.Mode != tc.wantMode || o.Host != tc.wantHost {
				t.Errorf("mode=%q host=%q, want mode=%q host=%q", o.Mode, o.Host, tc.wantMode, tc.wantHost)
			}
		})
	}
}

func TestALabelWithNoSshSchedulesTheNodeNotThisMachine(t *testing.T) {
	clearEnv(t)

	t.Setenv("KNO_HOST", "workstation")
	e := track.Env{
		Repo: testutil.RepoRoot(t),
		Config: config.Config{
			Network: "namenlos",
			Hosts: map[string]config.Host{
				"reed":  {Hostname: "reed", Scheduler: "timer"},
				"mixy":  {Hostname: "mixy", Scheduler: "none"},
				"quiet": {SSH: "quiet.example"},
			},
		},
	}
	o, err := options(e, nil, "reed", false)
	if err != nil {
		t.Fatal(err)
	}
	p, err := PlanFor(o)
	if err != nil {
		t.Fatal(err)
	}
	if p.Local() {
		t.Fatalf("the timer would be installed on this host, not on reed:\n%s",
			strings.Join(p.Commands(), "\n"))
	}
	got := strings.Join(p.Commands(), "\n")
	for _, want := range []string{
		"ssh reed " + sshx.AsRoot("kno activate --schedule=timer"),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "sudo install -m 0644 "+sysd(t, svcUnit)) {
		t.Errorf("a unit is being installed locally:\n%s", got)
	}
}

func TestARootLoginWithoutSudoSchedulesTheCronEntry(t *testing.T) {
	argv := plan(t, opts(t, ModeCron, "luscus.example")).Steps[0].Argv
	out, err := testutil.RemoteShell(t, 0, argv[len(argv)-1], "kno")
	if err != nil || !strings.Contains(out, "kno activate --schedule=cron") {
		t.Errorf("as root without sudo: printed %q (%v)", out, err)
	}
}

func TestAFreeBSDHostIsRefusedWithTheReason(t *testing.T) {
	clearEnv(t)
	e := track.Env{
		Repo:    testutil.RepoRoot(t),
		Network: "namenlos",
		Config: config.Config{
			Network: "namenlos",
			Hosts: map[string]config.Host{
				"respectmy": {SSH: "root@respectmy.example", Services: []string{"respectmy-pq-authority"}},
			},
		},
	}
	o, err := options(e, []string{ModeCron}, "respectmy", false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = PlanFor(o)
	if err == nil || !strings.Contains(err.Error(), "/usr/local/etc/cron.d/kno-update") {
		t.Errorf("a FreeBSD host got a Debian schedule, or no reason: %v", err)
	}
	if err := localCanSchedule("freebsd", ModeCron); err != nil {
		t.Errorf("kno schedule cron on a FreeBSD host itself was refused: %v", err)
	}
	if err := localCanSchedule("freebsd", ModeTimer); err == nil {
		t.Error("a systemd timer on FreeBSD was not refused")
	}
}

func TestCronOnAFreeBSDHostWritesTheJobAndItsLogRotation(t *testing.T) {
	p, err := PlanFor(Options{Mode: ModeCron, FreeBSD: true})
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]string{}
	for _, st := range p.Steps {
		if len(st.Argv) == 2 && st.Argv[0] == "tee" {
			body[st.Argv[1]] = st.Stdin
		}
	}
	if got := body["/usr/local/etc/cron.d/kno-update"]; !strings.Contains(got,
		"23,53 * * * * root /usr/local/bin/kno update >>/var/log/kno-update.log 2>&1\n") {
		t.Errorf("the cron file = %q", got)
	}
	if got := body["/usr/local/etc/newsyslog.conf.d/kno.conf"]; !strings.HasPrefix(got, "/var/log/kno-update.log\t") {
		t.Errorf("the newsyslog file = %q", got)
	}
	if p, err := PlanFor(Options{Mode: ModeNone, FreeBSD: true}); err != nil ||
		strings.Join(p.Commands(), " ") != "rm -f /usr/local/etc/cron.d/kno-update /usr/local/etc/newsyslog.conf.d/kno.conf" {
		t.Errorf("none = %q, %v", p.Commands(), err)
	}
	if _, err := PlanFor(Options{Mode: ModeTimer, FreeBSD: true}); err == nil {
		t.Error("a timer on FreeBSD was planned")
	}
}

func TestOptionsRejectsAMistypedMode(t *testing.T) {
	clearEnv(t)
	e := track.Env{Repo: testutil.RepoRoot(t)}
	_, err := options(e, []string{"hourly"}, "", false)
	if err == nil || !strings.Contains(err.Error(), "timer|cron|none") {
		t.Fatalf("expected an unknown-mode error, got %v", err)
	}
}

func TestOptionsSudoUnsetVersusEmpty(t *testing.T) {
	clearEnv(t)
	e := track.Env{Repo: testutil.RepoRoot(t)}

	os.Unsetenv("SUDO")
	o, err := options(e, []string{ModeNone}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	want := "sudo"
	if os.Geteuid() == 0 {
		want = ""
	}
	if o.Sudo != want {
		t.Errorf("an unset SUDO means %q for this account, got %q", want, o.Sudo)
	}

	t.Setenv("SUDO", "")
	if o, err = options(e, []string{ModeNone}, "", false); err != nil {
		t.Fatal(err)
	}
	if o.Sudo != "" {
		t.Errorf("SUDO= means no sudo, got %q", o.Sudo)
	}
}

func TestOptionsRejectsExtraArguments(t *testing.T) {
	clearEnv(t)
	e := track.Env{Repo: testutil.RepoRoot(t)}
	if _, err := options(e, []string{ModeNone, "host", "extra"}, "", false); err == nil {
		t.Fatal("expected an error for a third argument")
	}
}

func TestApplyRunsEveryStep(t *testing.T) {
	log := fakeTransports(t, "ssh", "scp")
	p := plan(t, opts(t, ModeTimer, "luscus.example"))
	var out strings.Builder
	if err := Apply(context.Background(), p, &out, false); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	ran := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	want := p.Commands()
	if len(ran) != len(want) {
		t.Fatalf("ran %d commands, planned %d:\n  ran     %q\n  planned %q", len(ran), len(want), ran, want)
	}
	for i := range want {
		if ran[i] != want[i] {
			t.Errorf("step %d:\n  ran     %s\n  planned %s", i, ran[i], want[i])
		}
	}
}

func TestApplyDryRunExecutesNothing(t *testing.T) {
	log := fakeTransports(t, "ssh", "scp")
	p := plan(t, opts(t, ModeTimer, "luscus.example"))
	var out strings.Builder
	if err := Apply(context.Background(), p, &out, true); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		b, _ := os.ReadFile(log)
		t.Fatalf("a dry run executed something: %s", b)
	}
	for _, c := range p.Commands() {
		if !strings.Contains(out.String(), "DRYRUN: "+c) {
			t.Errorf("dry run did not print %q:\n%s", c, out.String())
		}
	}
}

func TestApplyStopsOnFailure(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nexit 7\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	p := plan(t, opts(t, ModeTimer, "luscus.example"))
	var out strings.Builder
	if err := Apply(context.Background(), p, &out, false); err == nil {
		t.Fatal("a failing ssh must fail the schedule")
	}
}

func fakeTransports(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "argv.log")
	for _, n := range names {
		script := "#!/bin/sh\nprintf '%s %s\\n' \"$(basename \"$0\")\" \"$*\" >> " + log + "\n"
		if err := os.WriteFile(filepath.Join(dir, n), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"SSH", "SCP", "SUDO", "KNO_USER", "DRY_RUN", "KNO_HOST"} {
		t.Setenv(k, "")
	}
	os.Unsetenv("SUDO")
}

func TestTheUnitRunsAsTheUpdaterUser(t *testing.T) {
	for _, host := range []string{""} {
		o := opts(t, ModeTimer, host)
		o.User = "katzen"
		p := plan(t, o)
		body := p.Steps[0].Stdin
		if !strings.Contains(body, "User=katzen\n") {
			t.Errorf("host=%q: the unit ignores KNO_USER:\n%s", host, body)
		}
		if strings.Contains(body, "@KNO_") {
			t.Errorf("host=%q: a placeholder survived into an installed unit:\n%s", host, body)
		}
	}
}

func TestTheRemoteTimerGoesOnStdinNotThroughTmp(t *testing.T) {
	p := plan(t, opts(t, ModeTimer, "luscus.example"))
	for _, c := range p.Commands() {
		if strings.Contains(c, "/tmp") || strings.HasPrefix(c, "scp") {
			t.Errorf("the timer passes through a shared path on the host: %s", c)
		}
	}
}

func TestRemoteNoneFailsWhenTheRemoveFails(t *testing.T) {
	bin := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env, err := exec.LookPath("env")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(env, filepath.Join(bin, "env")); err != nil {
		t.Fatal(err)
	}
	write("id", "echo 0")
	write("kno", "exit 1")
	argv := plan(t, opts(t, ModeNone, "luscus.example")).Steps[0].Argv
	cmd := exec.Command("sh", "-c", argv[len(argv)-1])
	cmd.Env = []string{"PATH=" + bin}
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Errorf("kno activate failed on the host, and the remote command passed: %s", out)
	}
}

func TestABareScheduleOnAMachineThatIsNotAHostRefuses(t *testing.T) {
	clearEnv(t)
	t.Setenv("KNO_HOST", "elsewhere")
	e := track.Env{Repo: testutil.RepoRoot(t), Config: config.Config{
		Hosts: map[string]config.Host{"annares": {SSH: "root@annares", Scheduler: "timer"}},
	}}
	if o, err := options(e, nil, "", false); err == nil {
		t.Fatalf("a bare kno schedule on the organizer planned %q on this host", o.Mode)
	}
	if o, err := options(e, []string{ModeNone}, "", false); err != nil || o.Mode != ModeNone || o.Host != "" {
		t.Errorf("an explicit mode for this host: %+v %v", o, err)
	}
}

func TestADryRunSaysItWouldSet(t *testing.T) {
	clearEnv(t)
	t.Setenv("KNO_REPO", testutil.RepoRoot(t))
	cfg := filepath.Join(t.TempDir(), "kno.toml")
	if err := os.WriteFile(cfg, []byte("network = \"namenlos\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KNO_CONFIG", cfg)
	var out strings.Builder
	if rc := Run(&out, []string{ModeTimer, "luscus.example"}, "", true); rc != 0 {
		t.Fatalf("rc=%d:\n%s", rc, out.String())
	}
	if !strings.Contains(out.String(), "would be set on luscus.example") ||
		strings.Contains(out.String(), "' set on") {
		t.Errorf("a dry run says the scheduler is set:\n%s", out.String())
	}
}

func TestCronOnThisFreeBSDMachineIsTheFreeBSDCronJob(t *testing.T) {
	clearEnv(t)
	saved := localGOOS
	localGOOS = "freebsd"
	t.Cleanup(func() { localGOOS = saved })
	o, err := options(track.Env{Repo: testutil.RepoRoot(t), Network: "namenlos"}, []string{ModeCron}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !o.FreeBSD || o.Host != "" {
		t.Fatalf("options = %+v, want this FreeBSD machine", o)
	}
	p, err := PlanFor(o)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(p.Commands(), "\n"); !strings.Contains(got, "tee /usr/local/etc/cron.d/kno-update") ||
		strings.Contains(got, " /etc/cron.d/kno-update") {
		t.Errorf("plan = %s", got)
	}
}

func (p Plan) Commands() []string {
	out := make([]string, len(p.Steps))
	for i, s := range p.Steps {
		out[i] = strings.Join(s.Argv, " ")
	}
	return out
}
