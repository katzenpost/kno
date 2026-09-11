package hostsetup

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/schedule"
	"github.com/katzenpost/kno/internal/testutil"
	"github.com/katzenpost/kno/internal/track"
)

func testOptions() BootstrapOptions {
	o := DefaultBootstrapOptions("/repo")
	o.OperatorPubkey = "/keys/op.pub"
	return o
}

func staleRootForTest(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	staleRoot = root
	t.Cleanup(func() { staleRoot = "/" })
	return root
}

func put(t *testing.T, p, body string) {
	t.Helper()
	full := under(p)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func planLines(t *testing.T, o BootstrapOptions, f Facts) []string {
	t.Helper()

	if staleRoot == "/" {
		staleRootForTest(t)
	}
	p, err := BootstrapPlan(o, f)
	if err != nil {
		t.Fatalf("BootstrapPlan: %v", err)
	}
	return p.Lines()
}

func diffLines(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") == strings.Join(want, "\n") {
		return
	}
	for i := 0; i < len(got) || i < len(want); i++ {
		g, w := "", ""
		if i < len(got) {
			g = got[i]
		}
		if i < len(want) {
			w = want[i]
		}
		if g != w {
			t.Errorf("step %d:\n  got:  %s\n  want: %s", i, g, w)
		}
	}
}

func TestBootstrapPlanOnAFreshHost(t *testing.T) {
	want := []string{

		"useradd --system --home-dir /var/lib/kno-updater --create-home --shell /usr/bin/git-shell kno",
		"install -d -o kno -g kno /var/lib/kno-updater /var/lib/kno-updater/releases /var/lib/kno-updater/state",

		"git init --quiet --bare /srv/kno.git",
		"chown -R kno:kno /srv/kno.git",
		"write /srv/kno.git/hooks/post-receive (mode 0755)",
		"install -d -m 0700 -o kno -g kno /var/lib/kno-updater/.ssh",
		"append /keys/op.pub to /var/lib/kno-updater/.ssh/authorized_keys (mode 0600)",
		"runuser -u kno -- git clone --quiet https://github.com/katzenpost/katzenpost /var/lib/kno-updater/katzenpost",
		"install -d -m 0755 /etc/katzenpost",

		"write /etc/systemd/system/kno-update.service (mode 0644)",
		"write /etc/systemd/system/kno-update.timer (mode 0644)",

		"systemctl daemon-reload",
		"systemctl enable --now kno-update.timer",
	}
	diffLines(t, planLines(t, testOptions(),
		Facts{HasSystemd: true, GitShell: "/usr/bin/git-shell"}), want)
}

func TestWithoutGitShellThePushChannelIsNotBuilt(t *testing.T) {
	lines := planLines(t, testOptions(), Facts{HasSystemd: true})
	joined := strings.Join(lines, "\n")
	for _, absent := range []string{"git init --quiet --bare", "post-receive"} {
		if strings.Contains(joined, absent) {
			t.Errorf("built a push channel that cannot work (%q):\n%s", absent, joined)
		}
	}
	if !strings.Contains(joined, "--shell /usr/sbin/nologin") {
		t.Errorf("an account with no git to serve should keep nologin:\n%s", joined)
	}
}

func TestAnExistingNologinAccountIsGivenGitShell(t *testing.T) {
	lines := planLines(t, testOptions(), Facts{
		HasSystemd: true,
		UserExists: true,
		UserShell:  "/usr/sbin/nologin",
		GitShell:   "/usr/bin/git-shell",
	})
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "usermod --shell /usr/bin/git-shell kno") {
		t.Errorf("the account that cannot serve git was left as it was:\n%s", joined)
	}
	if strings.Contains(joined, "useradd") {
		t.Errorf("an existing account was recreated:\n%s", joined)
	}
}

func TestBootstrapPlanIsIdempotent(t *testing.T) {
	o := testOptions()
	o.Repo = testutil.RepoRoot(t)
	got := planLines(t, o, Facts{
		UserExists: true, BareRepoExists: true,
		KatzenpostCloned: true, HasSystemd: false,
		GitShell: "/usr/bin/git-shell", UserShell: "/usr/bin/git-shell",
	})
	for _, absent := range []string{
		"useradd", "git init", "git clone", "systemctl",
		"kno-update.service", "kno-update.timer",
	} {
		for _, l := range got {
			if strings.Contains(l, absent) {
				t.Errorf("second run would still do %q: %s", absent, l)
			}
		}
	}

	for _, present := range []string{
		"install -d -o kno -g kno", "chown -R kno:kno /srv/kno.git",
		"write /srv/kno.git/hooks/post-receive",
	} {
		if !strings.Contains(strings.Join(got, "\n"), present) {
			t.Errorf("second run does not repair %q", present)
		}
	}
}

func TestBootstrapWithoutPubkey(t *testing.T) {
	o := testOptions()
	o.OperatorPubkey = ""
	for _, l := range planLines(t, o, Facts{}) {
		if strings.Contains(l, ".ssh") {
			t.Errorf("unexpected ssh step: %s", l)
		}
	}
}

func TestPostReceiveHookContent(t *testing.T) {
	want := `#!/bin/sh
set -eu
work="/var/lib/kno-updater/network-organizer"
if [ -d "$work/.git" ]; then
	git -C "$work" fetch --tags --prune --quiet origin
else
	git clone --quiet "/srv/kno.git" "$work"
fi
`
	if got := PostReceiveHook("/var/lib/kno-updater", "/srv/kno.git"); got != want {
		t.Errorf("hook differs:\n  got:\n%q\n  want:\n%q", got, want)
	}
}

func fileStep(t *testing.T, p Plan, path string) File {
	t.Helper()
	for _, s := range p {
		if s.File != nil && s.File.Path == path {
			return *s.File
		}
	}
	t.Fatalf("no step writes %s", path)
	return File{}
}

func TestBootstrapFileContents(t *testing.T) {
	o := testOptions()
	o.Privilege = config.PrivKno
	p, err := BootstrapPlan(o, Facts{HasSystemd: true, GitShell: "/usr/bin/git-shell"})
	if err != nil {
		t.Fatal(err)
	}
	sudoers := fileStep(t, p, SudoersPath)

	for _, want := range []string{
		"/usr/bin/kno activate *",
		"/usr/bin/kno doctor",
		"/usr/bin/dpkg -i *",
		"systemctl restart *",
		"systemctl reset-failed *",
		"/usr/bin/rsync",
	} {
		if !strings.Contains(sudoers.Content, want) {
			t.Errorf("sudoers does not allow %q:\n%s", want, sudoers.Content)
		}
	}

	for _, never := range []string{
		"ALL=(root) NOPASSWD: ALL", "/bin/sh", "/bin/bash", "/usr/bin/tee",
		"/usr/bin/vi", "/usr/bin/find", "/usr/bin/cp",
	} {
		if strings.Contains(sudoers.Content, never) {
			t.Errorf("sudoers hands out %q, which is a root shell in one step:\n%s",
				never, sudoers.Content)
		}
	}

	if sudoers.Mode != 0o440 {
		t.Errorf("sudoers mode = %04o, want 0440", sudoers.Mode)
	}
	svc := fileStep(t, p, ServicePath)

	if want := `[Unit]
Description=katzenpost network organizer: check for and apply updates
Wants=network-online.target
After=network-online.target

[Service]
Type=oneshot
User=kno
ExecStart=/usr/bin/kno update
Nice=10
`; svc.Content != want {
		t.Errorf("service = %q, want %q", svc.Content, want)
	}

	if strings.Contains(svc.Content, "@KNO_") {
		t.Errorf("an unsubstituted placeholder would be installed: %q", svc.Content)
	}
	tmr := fileStep(t, p, TimerPath)
	want, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "systemd", "kno-update.timer"))
	if err != nil {
		t.Fatal(err)
	}
	if tmr.Content != string(want) {
		t.Errorf("timer = %q, want %q", tmr.Content, want)
	}
	hook := fileStep(t, p, "/srv/kno.git/hooks/post-receive")
	if hook.Mode != 0o755 || hook.Owner != "kno:kno" {
		t.Errorf("hook mode %04o owner %q", hook.Mode, hook.Owner)
	}
}

func TestBootstrapWritesNoLegacyConfig(t *testing.T) {
	repo := testutil.RepoRoot(t)
	if _, err := os.Stat(filepath.Join(repo, "examples", "kno.conf")); err != nil {
		t.Fatalf("the legacy example is gone, so this test proves nothing: %v", err)
	}
	for _, pkg := range []bool{false, true} {
		for _, f := range []Facts{{}, {HasSystemd: true}, {HasSystemd: true, GitShell: "/usr/bin/git-shell"}} {
			o := DefaultBootstrapOptions(repo)
			o.PackageInstall = pkg
			for _, l := range planLines(t, o, f) {
				if strings.Contains(l, "kno.conf") {
					t.Errorf("package=%v: bootstrap writes the legacy config, and its example names"+
						" another node's services: %s", pkg, l)
				}
			}
		}
	}
}

func TestApplyRunsTheSteps(t *testing.T) {
	tmp := t.TempDir()
	marker := filepath.Join(tmp, "marker")
	p := Plan{
		{Desc: "write a file", File: &File{Path: filepath.Join(tmp, "d", "f"), Content: "hello\n", Mode: 0o640}},
		{Desc: "touch a marker", Argv: []string{"touch", marker}},
		{Desc: "a step that fails", Argv: []string{"false"}, BestEffort: true},
	}
	var log bytes.Buffer
	if err := p.Apply(t.Context(), &log); err != nil {
		t.Fatalf("Apply: %v\n%s", err, log.String())
	}
	b, err := os.ReadFile(filepath.Join(tmp, "d", "f"))
	if err != nil || string(b) != "hello\n" {
		t.Errorf("file not written: %v %q", err, b)
	}
	fi, err := os.Stat(filepath.Join(tmp, "d", "f"))
	if err != nil || fi.Mode().Perm() != 0o640 {
		t.Errorf("mode not applied: %v %v", err, fi.Mode())
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("command step did not run: %v", err)
	}
	if !strings.Contains(log.String(), "a step that fails failed, continuing") {
		t.Errorf("best-effort failure was not logged:\n%s", log.String())
	}

	strict := Plan{{Desc: "a step that fails", Argv: []string{"false"}}}
	if err := strict.Apply(t.Context(), &log); err == nil {
		t.Error("a failing step did not stop the plan")
	}
}

func TestBootstrapDepsAreBestEffort(t *testing.T) {
	o := testOptions()
	o.Deps = &DepsOptions{Profile: ProfileFull, Manager: ManagerApt, Packages: DebianPackages()}
	staleRootForTest(t)

	var deps Plan
	p, _, err := bootstrap(o, func(BootstrapOptions) Facts { return Facts{} },
		func(d Plan) error { deps = d; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 2 {
		t.Fatalf("deps = %v, want apt update and apt install", deps.Lines())
	}
	for _, s := range deps {
		if !strings.Contains(s.String(), "apt") || !s.BestEffort {
			t.Errorf("dependency step is not a best-effort apt step: %s", s)
		}
	}
	for _, s := range p {
		if s.BestEffort && strings.Contains(s.String(), "apt") {
			t.Errorf("the bootstrap plan runs the dependencies again: %s", s)
		}
	}
	if p[0].BestEffort {
		t.Errorf("a bootstrap step was marked best effort: %s", p[0])
	}
}

func TestTheDependenciesInstallBeforeTheProbe(t *testing.T) {
	o := testOptions()
	o.Deps = &DepsOptions{Profile: ProfileFull, Manager: ManagerApt, Packages: DebianPackages()}
	staleRootForTest(t)

	gitInstalled := false
	probe := func(BootstrapOptions) Facts {
		if gitInstalled {
			return Facts{HasSystemd: true, GitShell: "/usr/bin/git-shell"}
		}
		return Facts{HasSystemd: true}
	}
	p, f, err := bootstrap(o, probe, func(Plan) error { gitInstalled = true; return nil })
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(p.Lines(), "\n")
	for _, want := range []string{"git init --quiet --bare /srv/kno.git", "--shell /usr/bin/git-shell"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the plan was made before the dependencies installed git (%q missing):\n%s", want, joined)
		}
	}
	if !strings.Contains(nextSteps(o, f), "git push") {
		t.Errorf("the push hint is missing with a bare repo: %s", nextSteps(o, f))
	}
	if got := nextSteps(o, Facts{}); strings.Contains(got, "git push") {
		t.Errorf("bootstrap tells the operator to git push to a repo it did not make: %s", got)
	}
}

func TestTheLastHintNamesTheConfigKnoReads(t *testing.T) {
	o := testOptions()
	for _, f := range []Facts{{}, {GitShell: "/usr/bin/git-shell"}} {
		got := nextSteps(o, f)
		if !strings.Contains(got, SystemConfPath) || strings.Contains(got, "kno.conf") ||
			strings.Contains(got, "SERVICES") {
			t.Errorf("the hint points at the legacy config: %s", got)
		}
	}
}

func TestAKeyIsAuthorizedOnce(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "op.pub")
	if err := os.WriteFile(key, []byte("ssh-ed25519 AAAAop op@laptop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	keys := filepath.Join(dir, "authorized_keys")
	if err := os.WriteFile(keys, []byte("ssh-ed25519 AAAAother other@box"), 0o600); err != nil {
		t.Fatal(err)
	}
	step := Plan{{Desc: "authorize", File: &File{Path: keys, Source: key, Mode: 0o600, Append: true}}}
	for i := 0; i < 2; i++ {
		if err := step.Apply(t.Context(), io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(keys)
	if err != nil {
		t.Fatal(err)
	}
	want := "ssh-ed25519 AAAAother other@box\nssh-ed25519 AAAAop op@laptop\n"
	if string(b) != want {
		t.Errorf("authorized_keys = %q, want %q", b, want)
	}
}

func TestSudoersCitesOnlyFilesThatExist(t *testing.T) {
	root := testutil.ModuleRoot(t)
	for _, f := range strings.Fields(Sudoers("kno", "/usr/bin")) {
		if strings.HasPrefix(f, "docs/") {
			if _, err := os.Stat(filepath.Join(root, f)); err != nil {
				t.Errorf("the sudoers header cites %s: %v", f, err)
			}
		}
	}
}

func TestClientWantedReadsTheDefaults(t *testing.T) {
	t.Setenv("KNO_HOST", "mixy")
	c := config.Config{Hosts: map[string]config.Host{"mixy": {}}}
	c.Defaults.Kpclientd = config.Bool(false)
	e := track.Env{Services: []string{"mixy-pq-mixserver"}, Config: c}
	if clientWanted(e) {
		t.Error("bootstrap starts kpclientd on a host that [defaults] says has none")
	}
	c.Hosts["mixy"] = config.Host{Kpclientd: config.Bool(true)}
	e.Config = c
	if !clientWanted(e) {
		t.Error("the host's own kpclientd = true lost to [defaults]")
	}
}

func TestBothInstallersWriteTheSameUnits(t *testing.T) {
	o := testOptions()
	o.User = "katzen"
	staleRootForTest(t)
	p, err := BootstrapPlan(o, Facts{HasSystemd: true})
	if err != nil {
		t.Fatal(err)
	}
	sp, err := schedule.PlanFor(schedule.Options{
		Repo: testutil.RepoRoot(t), Mode: schedule.ModeTimer, Host: "node.example",
		SSH: "ssh", User: o.User,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := fileStep(t, p, ServicePath).Content, sp.Steps[0].Stdin; got != want {
		t.Errorf("bootstrap writes the service\n%s\nand kno schedule writes\n%s", got, want)
	}
	if got, want := fileStep(t, p, TimerPath).Content, sp.Steps[1].Stdin; got != want {
		t.Errorf("bootstrap writes the timer\n%s\nand kno schedule writes\n%s", got, want)
	}
}

func gitRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@invalid",
			"-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "--quiet")
	if err := os.WriteFile(filepath.Join(dir, "network.toml"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "network.toml")
	run("commit", "--quiet", "-m", "one")
	return dir, run("rev-parse", "HEAD")
}

func TestRevNeedsACleanCheckoutAtThatRevision(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	if err := checkRevision(t.TempDir(), "0123abcd"); err == nil {
		t.Error("--rev passed in a directory that is not a git checkout")
	}
	dir, head := gitRepo(t)
	if err := checkRevision(dir, head); err != nil {
		t.Fatalf("a clean checkout at --rev was refused: %v", err)
	}
	if err := checkRevision(dir, "0123abcd"); err == nil {
		t.Error("a checkout at another revision passed")
	}
	if err := os.WriteFile(filepath.Join(dir, "network.toml"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkRevision(dir, head); err == nil {
		t.Error("an edited file passed --rev")
	}
	if err := os.WriteFile(filepath.Join(dir, "network.toml"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.toml"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkRevision(dir, head); err == nil {
		t.Error("a file that is not in the revision passed --rev")
	}
	if err := checkRevision(dir, ""); err != nil {
		t.Errorf("without --rev there is nothing to check: %v", err)
	}
}

func TestBootstrapGrantsNoSubidRange(t *testing.T) {
	for _, f := range []Facts{
		{HasSystemd: true},
		{HasSystemd: true, UserExists: true, GitShell: "/usr/bin/git-shell"},
		{},
	} {
		for _, l := range planLines(t, testOptions(), f) {
			if strings.Contains(l, "--add-subuids") || strings.Contains(l, "--add-subgids") {
				t.Errorf("bootstrap grants a subordinate id range: %s", l)
			}
		}
	}
}

func TestBootstrapPlanRejectsIncompleteOptions(t *testing.T) {
	_, err := BootstrapPlan(BootstrapOptions{Repo: "/repo"}, Facts{})
	if err == nil {
		t.Fatal("expected an error for unset options")
	}
	for _, want := range []string{"User", "Home", "Bare", "BinDir"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %s: %v", want, err)
		}
	}
}

func TestBootstrapStartsTheClientDaemonWhenTheHostWantsOne(t *testing.T) {
	o := DefaultBootstrapOptions(t.TempDir())
	p, err := BootstrapPlan(o, Facts{HasSystemd: true, Kpclientd: true})
	if err != nil {
		t.Fatal(err)
	}
	var enable, reset bool
	for _, s := range p {
		line := strings.Join(s.Argv, " ")
		if strings.Contains(line, "enable --now kpclientd.service") {
			enable = true
			if !s.BestEffort {
				t.Error("a client daemon that will not start must not fail the install")
			}
		}
		if strings.Contains(line, "reset-failed kpclientd.service") {
			reset = true
		}
	}
	if !enable {
		t.Error("bootstrap does not start the client daemon")
	}

	if !reset {
		t.Error("bootstrap does not clear an earlier failure first")
	}
}

func TestBootstrapStartsNoClientDaemonWhenTheHostDidNotAsk(t *testing.T) {
	p, err := BootstrapPlan(DefaultBootstrapOptions(t.TempDir()), Facts{HasSystemd: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range p {
		if strings.Contains(strings.Join(s.Argv, " "), "kpclientd") {
			t.Errorf("bootstrap touches kpclientd on a host that did not ask: %q", s.Argv)
		}
	}
}

func TestPrivilegeModeDecidesSudoers(t *testing.T) {
	cases := []struct {
		mode  string
		grant bool
	}{
		{"", false},
		{config.PrivNone, false},
		{config.PrivApt, false},
		{config.PrivSudo, false},
		{config.PrivKno, true},
		{"nonsense-that-is-not-a-mode", false},
	}
	for _, c := range cases {
		staleRootForTest(t)

		put(t, SudoersPath, "kno ALL=(root) NOPASSWD: /usr/bin/kno activate *\n")
		o := testOptions()
		o.Privilege = c.mode
		lines := strings.Join(planLines(t, o, Facts{HasSystemd: true}), "\n")
		writes := strings.Contains(lines, "write "+SudoersPath)
		removes := strings.Contains(lines, "rm -f "+SudoersPath)
		if c.grant && !writes {
			t.Errorf("privilege=%q should write the sudoers rule", c.mode)
		}
		if !c.grant {
			if writes {
				t.Errorf("privilege=%q must not write a sudoers rule", c.mode)
			}
			if !removes {
				t.Errorf("privilege=%q must remove any sudoers rule already there", c.mode)
			}
		}
	}
}

func TestNoSudoersRuleMeansNoRemovalStep(t *testing.T) {
	staleRootForTest(t)
	o := testOptions()
	o.Privilege = config.PrivNone
	lines := strings.Join(planLines(t, o, Facts{HasSystemd: true}), "\n")
	if strings.Contains(lines, "rm -f "+SudoersPath) {
		t.Errorf("the plan removes a sudoers rule that is not there:\n%s", lines)
	}
}

func TestTheUpdaterUnitIsWrittenOnce(t *testing.T) {
	staleRootForTest(t)

	put(t, ServicePath, "[Service]\nExecStart=/usr/local/bin/kno update\n")
	lines := planLines(t, testOptions(), Facts{HasSystemd: true})
	n := 0
	for _, ln := range lines {
		if strings.Contains(ln, "write "+ServicePath) {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the updater unit is written %d times, want once:\n%s",
			n, strings.Join(lines, "\n"))
	}
}

func TestWithoutSystemdRepairStillRewritesTheUnit(t *testing.T) {
	staleRootForTest(t)
	put(t, ServicePath, "[Service]\nExecStart=/usr/local/bin/kno update\n")
	lines := strings.Join(planLines(t, testOptions(), Facts{}), "\n")
	if !strings.Contains(lines, "write "+under(ServicePath)) {
		t.Errorf("a stale unit was left alone on a host with no systemd:\n%s", lines)
	}
}

func TestManualHostStillBuildsAndWitnesses(t *testing.T) {
	o := testOptions()
	o.Privilege = config.PrivNone
	lines := strings.Join(planLines(t, o, Facts{HasSystemd: true}), "\n")
	for _, want := range []string{
		"useradd --system", "/var/lib/kno-updater/releases",
		"kno-update.service", "kno-update.timer",
	} {
		if !strings.Contains(lines, want) {
			t.Errorf("a manual host still needs %q:\n%s", want, lines)
		}
	}
}
