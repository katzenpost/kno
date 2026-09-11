package doctor

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/hostsetup"

	"github.com/katzenpost/kno/internal/status"
	"github.com/katzenpost/kno/internal/track"
	"github.com/katzenpost/kno/internal/verify"
)

func oneeyedmanFault() status.UserFault {
	return status.UserFault{
		Service:  "oneeyedman-pq-mixserver",
		Unit:     "pq-katzenpost-mixserver",
		User:     "pq-katzenpost-mixserver",
		StateDir: "/var/lib/pq-katzenpost-mixserver",
		Config:   "/etc/katzenpost.toml",
	}
}

func TestFixNeverRemovesAFileOrRunsKno(t *testing.T) {
	f := hostFacts{Faults: []status.UserFault{oneeyedmanFault()}, Stale: []string{"/usr/local/bin/kno-update"}}
	for _, fx := range hostFixes(f) {
		for _, argv := range fx.Commands {
			if destructive(argv) {
				t.Errorf("--fix would run something destructive: %v", argv)
			}
			if filepath.Base(argv[0]) == "kno" {
				t.Errorf("--fix runs kno, which removes files: %v", argv)
			}
		}
	}
	for _, argv := range [][]string{
		{"kno", "bootstrap", "--package"},
		{"/usr/bin/kno", "bootstrap", "--package"},
	} {
		if !destructive(argv) {
			t.Errorf("destructive allows %v, which removes files and sudoers", argv)
		}
	}
}

func TestFixPrintsTheCommandForOldKnoFiles(t *testing.T) {
	var b bytes.Buffer
	f := hostFacts{Label: "mixy", UpdaterUser: "kno", Stale: []string{"/usr/local/bin/kno-update"}}
	if err := ApplyFixes(context.Background(), &b, track.Env{}, f, true); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "sudo KNO_USER=kno kno bootstrap --package") {
		t.Errorf("--fix does not say how to remove the old files:\n%s", out)
	}
	if strings.Contains(out, "looks healthy") {
		t.Errorf("--fix calls a host with old kno files healthy:\n%s", out)
	}
}

func TestDestructiveCatchesTheDangerousOnes(t *testing.T) {
	for _, argv := range [][]string{
		{"rm", "-rf", "/var/lib/pq-katzenpost-mixserver"},
		{"rmdir", "/x"},
		{"userdel", "pq-katzenpost-mixserver"},
		{"truncate", "-s0", "/etc/katzenpost.toml"},
		{"sh", "-c", "echo x > /etc/katzenpost.toml"},
		{"chown", "a:b", "/x;rm -rf /"},
	} {
		if !destructive(argv) {
			t.Errorf("destructive missed %v", argv)
		}
	}
	for _, argv := range [][]string{
		{"groupadd", "--system", "kno"},
		{"chown", "-R", "a:b", "/var/lib/x"},
		{"systemctl", "restart", "pq-katzenpost-mixserver"},
	} {
		if destructive(argv) {
			t.Errorf("destructive flagged a safe command: %v", argv)
		}
	}
}

func TestFixMatchesWhatStatusPrints(t *testing.T) {
	fault := oneeyedmanFault()
	printed := strings.Join(fault.Repair(), "\n")
	var ran []string
	for _, argv := range userFaultArgv(fault) {
		ran = append(ran, strings.Join(argv, " "))
	}
	joined := strings.Join(ran, "\n")
	for _, want := range []string{
		"groupadd --system pq-katzenpost-mixserver",
		"useradd --system --gid pq-katzenpost-mixserver",
		"chown -R pq-katzenpost-mixserver:pq-katzenpost-mixserver /var/lib/pq-katzenpost-mixserver",
		"systemctl restart pq-katzenpost-mixserver",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("--fix does not run %q:\n%s", want, joined)
		}
		if !strings.Contains(printed, want) {
			t.Errorf("status does not print %q", want)
		}
	}

	for _, argv := range userFaultArgv(fault) {
		for _, a := range argv {
			if strings.ContainsAny(a, "&|;") {
				t.Errorf("--fix argv contains shell syntax: %v", argv)
			}
		}
	}
}

func TestDryRunChangesNothingAndSaysSo(t *testing.T) {
	var b bytes.Buffer
	f := hostFacts{Faults: []status.UserFault{oneeyedmanFault()}}
	if err := ApplyFixes(context.Background(), &b, track.Env{}, f, true); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "nothing was changed") {
		t.Errorf("a dry run does not say it changed nothing:\n%s", out)
	}
	if !strings.Contains(out, "groupadd --system pq-katzenpost-mixserver") {
		t.Errorf("a dry run does not show the commands:\n%s", out)
	}
}

func TestNothingToFixIsNotAnError(t *testing.T) {
	var b bytes.Buffer
	if err := ApplyFixes(context.Background(), &b, track.Env{}, hostFacts{}, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "knows how to repair") {
		t.Errorf("a healthy host should say so: %s", b.String())
	}
}

func TestNothingToFixStillNamesWhatIsDown(t *testing.T) {
	var b bytes.Buffer
	f := hostFacts{Units: []unitReport{{
		Service: "oneeyedman-pq-mixserver", Unit: "pq-katzenpost-mixserver",
		Installed: true, State: verify.UnitState{Active: "activating"},
	}}}
	if err := ApplyFixes(context.Background(), &b, track.Env{}, f, true); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"pq-katzenpost-mixserver", "is not running", "kno doctor", "kno update"} {
		if !strings.Contains(out, want) {
			t.Errorf("a dead end: %q is not in the output:\n%s", want, out)
		}
	}
}

func TestNothingToFixOnAHealthyHostSaysSo(t *testing.T) {
	var b bytes.Buffer
	f := hostFacts{Units: []unitReport{{
		Service: "oneeyedman-pq-mixserver", Unit: "pq-katzenpost-mixserver",
		Installed: true, State: verify.UnitState{Active: "active"},
	}}}
	if err := ApplyFixes(context.Background(), &b, track.Env{}, f, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "looks healthy") {
		t.Errorf("a healthy host should be told so:\n%s", b.String())
	}
}

func TestSystemdExitStatusesAreDecoded(t *testing.T) {
	for status, want := range map[int]string{
		217: "User= account does not exist",
		203: "could not execute the binary",
		200: "working directory",
		232: "state directory",
	} {
		got := systemdExit[status]
		if !strings.Contains(got, want) {
			t.Errorf("status %d decodes to %q, want something mentioning %q", status, got, want)
		}
	}

	if m, ok := systemdExit[1]; ok {
		t.Errorf("exit status 1 is the service's own, not systemd's; decoded as %q", m)
	}
}

func TestExecStartPreParsesSystemdsFormat(t *testing.T) {
	const prop = `{ path=/usr/local/bin/pq-katzenpost-mixserver ; argv[]=/usr/local/bin/pq-katzenpost-mixserver -f /etc/katzenpost.toml --validate-only ; ignore_errors=no ; start_time=[n/a] ; stop_time=[n/a] ; pid=0 ; code=(null) ; status=0/0 }`
	got := parseExecArgv(prop)
	want := []string{
		"/usr/local/bin/pq-katzenpost-mixserver",
		"-f", "/etc/katzenpost.toml", "--validate-only",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("parsed %v, want %v", got, want)
	}
	if n := parseExecArgv(""); n != nil {
		t.Errorf("an empty property should parse to nothing, got %v", n)
	}
	if n := parseExecArgv("{ path=/x ; ignore_errors=no }"); n != nil {
		t.Errorf("a property with no argv should parse to nothing, got %v", n)
	}
}

func TestFixDisablesKpclientdWithNoConfig(t *testing.T) {
	f := hostFacts{ClientInstalled: true, ClientConfig: false}
	f.Client.Active = "activating"
	got := hostFixes(f)
	found := false
	for _, fx := range got {
		for _, argv := range fx.Commands {
			if strings.Join(argv, " ") == "systemctl disable --now kpclientd" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("no disable for a kpclientd that cannot start: %+v", got)
	}
}

func TestFixLeavesKpclientdAloneWhenItHasAConfig(t *testing.T) {
	for _, c := range []struct {
		name string
		f    hostFacts
	}{
		{"has a config", hostFacts{ClientInstalled: true, ClientConfig: true}},
		{"not installed", hostFacts{ClientInstalled: false, ClientConfig: false}},
	} {
		f := c.f
		f.Client.Active = "activating"
		for _, fx := range hostFixes(f) {
			for _, argv := range fx.Commands {
				if strings.Contains(strings.Join(argv, " "), "disable") {
					t.Errorf("%s: must not disable kpclientd: %v", c.name, argv)
				}
			}
		}
	}

	f := hostFacts{ClientInstalled: true, ClientConfig: false}
	f.Client.Active = "active"
	for _, fx := range hostFixes(f) {
		for _, argv := range fx.Commands {
			if strings.Contains(strings.Join(argv, " "), "disable") {
				t.Errorf("an active kpclientd must not be disabled: %v", argv)
			}
		}
	}
}

func TestFixEnablesKpclientdThatHasItsConfig(t *testing.T) {
	f := hostFacts{ClientInstalled: true, ClientConfig: true, ClientWanted: true}
	f.Client.Enabled = false
	f.Client.Active = "inactive"
	got := ""
	for _, fx := range hostFixes(f) {
		for _, argv := range fx.Commands {
			if strings.Contains(strings.Join(argv, " "), "kpclientd") {
				got = strings.Join(argv, " ")
			}
		}
	}
	if got != "systemctl enable --now kpclientd" {
		t.Fatalf("got %q, want enable --now: a disabled daemon with a config must be turned on", got)
	}

	f.Client.Enabled = true
	got = ""
	for _, fx := range hostFixes(f) {
		for _, argv := range fx.Commands {
			if strings.Contains(strings.Join(argv, " "), "kpclientd") {
				got = strings.Join(argv, " ")
			}
		}
	}
	if got != "systemctl start kpclientd" {
		t.Fatalf("got %q, want start", got)
	}

	f.ClientConfig = false
	f.Client.Enabled = true
	for _, fx := range hostFixes(f) {
		for _, argv := range fx.Commands {
			if strings.Contains(strings.Join(argv, " "), "enable") {
				t.Errorf("must not enable a daemon with no config: %v", argv)
			}
		}
	}
}

func TestFixInstallsAMissingProgramItIsAllowedTo(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS has no system package manager for kno doctor --fix to drive")
	}
	got := missingToolFixes([]Status{
		{Name: "rsync", Program: "rsync", Scope: ScopePush, Checked: true, Found: false,
			Tool: Tool{Name: "rsync", Install: "rsync"}},
	})
	if len(got) != 1 {
		t.Fatalf("a missing allow-listed program produced no fix: %+v", got)
	}
	if !strings.Contains(got[0].What, "rsync") {
		t.Errorf("What = %q, want it to name rsync", got[0].What)
	}

	flat := ""
	for _, argv := range got[0].Commands {
		flat += strings.Join(argv, " ") + "\n"
	}
	if !strings.Contains(flat, " -y ") {
		t.Errorf("an interactive install would hang --fix:\n%s", flat)
	}
}

func TestEveryManagerInstallsWithoutAsking(t *testing.T) {
	for mgr, want := range map[string]string{
		hostsetup.ManagerApt: "env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends rsync",
		hostsetup.ManagerPkg: "pkg install -y rsync",
	} {
		argv := installArgv(mgr, []string{"rsync"})
		if got := strings.Join(argv[len(argv)-1], " "); got != want {
			t.Errorf("%s: %q, want %q", mgr, got, want)
		}
	}
}

func TestFixWillNotInstallWhatItWasNotAllowedTo(t *testing.T) {
	got := missingToolFixes([]Status{
		{Name: "pq-katzenpost-fetch", Program: "pq-katzenpost-fetch", Scope: ScopeNode,
			Checked: true, Found: false, Tool: Tool{Name: "pq-katzenpost-fetch"}},
		{Name: "docker", Program: "docker", Scope: ScopeNode,
			Checked: true, Found: false, Tool: Tool{Name: "docker"}},
	})
	if len(got) != 0 {
		t.Errorf("--fix proposed installing programs kno provides or does not need: %+v", got)
	}
}

func TestFixLeavesPresentProgramsAlone(t *testing.T) {
	got := missingToolFixes([]Status{
		{Name: "rsync", Program: "rsync", Scope: ScopePush, Checked: true, Found: true,
			Tool: Tool{Name: "rsync", Install: "rsync"}},
	})
	if len(got) != 0 {
		t.Errorf("a present program produced a fix: %+v", got)
	}
}

func TestASupersedeRepairCarriesItsOwnUndo(t *testing.T) {
	f := hostFacts{Units: []unitReport{{
		Service: "haywood-pq-gatewaynode", Unit: "pq-katzenpost-mixserver",
		Installed: true, Shadow: "/etc/systemd/system/pq-katzenpost-mixserver.service",
	}}}
	var fx *fixable
	for i, c := range hostFixes(f) {
		if strings.Contains(c.What, "not the packaged unit") {
			fx = &hostFixes(f)[i]
		}
	}
	if fx == nil {
		t.Fatal("no supersede repair was produced")
	}
	if len(fx.Units) != 1 || fx.Units[0] != "pq-katzenpost-mixserver" {
		t.Errorf("Units = %q; without them the repair cannot be checked", fx.Units)
	}
	if len(fx.Undo) == 0 {
		t.Fatal("a repair that restarts a service must carry its reversal")
	}

	first := strings.Join(fx.Undo[0], " ")
	if !strings.Contains(first, supersededSuffix) || !strings.HasPrefix(first, "mv ") {
		t.Errorf("undo does not restore the moved file: %s", first)
	}
	if fx.Undo[0][3] != "/etc/systemd/system/pq-katzenpost-mixserver.service" {
		t.Errorf("undo restores to %q, not where the file came from", fx.Undo[0][3])
	}

	for _, argv := range append(append([][]string{}, fx.Commands...), fx.Undo...) {
		if destructive(argv) {
			t.Errorf("a repair or its undo can destroy something: %v", argv)
		}
	}
}

func TestFixesComeFromTheGatheredToolsNotThisMachine(t *testing.T) {
	if _, err := hostsetup.DetectManager(); err != nil {
		t.Skip("no package manager here to name an install command")
	}
	missing := []Status{{Name: "rsync", Checked: true, Tool: Tool{Install: "rsync"}}}
	got := hostFixes(hostFacts{Tools: missing})
	if len(got) == 0 || !strings.Contains(got[0].What, "install rsync") {
		t.Errorf("the gathered facts name rsync as missing, but the fixes are %v", got)
	}
	if got := hostFixes(hostFacts{}); len(got) != 0 {
		t.Errorf("no tool was gathered as missing, but the fixes are %v", got)
	}
}

func TestFixKeepsAKpclientdThatTheConfigWants(t *testing.T) {
	f := hostFacts{ClientInstalled: true, ClientConfig: false, ClientWanted: true}
	f.Client.Active = "activating"
	for _, fx := range hostFixes(f) {
		for _, argv := range fx.Commands {
			if strings.Contains(strings.Join(argv, " "), "disable") {
				t.Errorf("kno.toml wants kpclientd on this host, and --fix disables it: %v", argv)
			}
		}
	}
}

func kpclientdArgv(f hostFacts) []string {
	var out []string
	for _, fx := range hostFixes(f) {
		for _, argv := range fx.Commands {
			if a := strings.Join(argv, " "); strings.Contains(a, "kpclientd") || strings.HasPrefix(a, "service jail start ") {
				out = append(out, strings.Join(argv, " "))
			}
		}
	}
	return out
}

func TestFixDoesNotStartAKpclientdThatTheConfigDoesNotWant(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		f := hostFacts{ClientInstalled: true, ClientConfig: true, ClientWanted: false}
		f.Client.Enabled, f.Client.Active = enabled, "inactive"
		if got := kpclientdArgv(f); len(got) != 0 {
			t.Errorf("enabled=%v: kno.toml sets kpclientd = false, and --fix runs %q", enabled, got)
		}
	}
}

func TestFixStartsAJailedKpclientdInItsJail(t *testing.T) {
	f := hostFacts{ClientInstalled: true, ClientConfig: true, ClientWanted: true, ClientJail: "kpclient"}
	f.Client.Enabled, f.Client.Active = true, "inactive"
	got := kpclientdArgv(f)
	if len(got) != 1 || got[0] != "service jail start kpclient" {
		t.Errorf("--fix runs %q, want the client jail started", got)
	}
}

func TestFixDoesNotStartAMaskedKpclientd(t *testing.T) {
	f := hostFacts{ClientInstalled: true, ClientConfig: true, ClientWanted: true, ClientMasked: true}
	f.Client.Active = "inactive"
	if got := kpclientdArgv(f); len(got) != 0 {
		t.Errorf("--fix runs %q on a masked kpclientd", got)
	}
}

func TestInstancesOfOneTemplateGetOneRepair(t *testing.T) {
	const shadow = "/etc/systemd/system/kpserver@.service"
	f := hostFacts{Units: []unitReport{
		{Unit: "kpserver@reed-pq-mixserver", Installed: true, Shadow: shadow},
		{Unit: "kpserver@reed-pq-gatewaynode", Installed: true, Shadow: shadow},
	}}
	var moves, repairs int
	for _, fx := range hostFixes(f) {
		repairs++
		if len(fx.Units) != 2 {
			t.Errorf("the repair checks %q, want both instances", fx.Units)
		}
		for _, argv := range fx.Commands {
			if argv[0] == "mv" {
				moves++
			}
		}
		last := strings.Join(fx.Commands[len(fx.Commands)-1], " ")
		if last != "systemctl restart kpserver@reed-pq-mixserver kpserver@reed-pq-gatewaynode" {
			t.Errorf("the repair ends with %q, want a restart of every instance", last)
		}
	}
	if repairs != 1 || moves != 1 {
		t.Errorf("%d repairs with %d moves for one file, want 1 and 1", repairs, moves)
	}
}

func fakeRoot(t *testing.T, run func(argv []string) error) *[]string {
	t.Helper()
	var ran []string
	oldRun, oldEuid := runArgv, euid
	runArgv = func(_ context.Context, _ io.Writer, argv []string) error {
		ran = append(ran, strings.Join(argv, " "))
		return run(argv)
	}
	euid = func() int { return 0 }
	t.Cleanup(func() { runArgv, euid = oldRun, oldEuid })
	return &ran
}

func exitWith(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("/bin/sh", "-c", "exit "+strconv.Itoa(code)).Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("no exit error: %v", err)
	}
	return err
}

func TestAFailedRestartPutsTheFileBack(t *testing.T) {
	const shadow = "/etc/systemd/system/pq-katzenpost-mixserver.service"
	failed := false
	ran := fakeRoot(t, func(argv []string) error {
		if argv[1] == "restart" && !failed {
			failed = true
			return exitWith(t, 1)
		}
		return nil
	})
	f := hostFacts{Units: []unitReport{{Unit: "pq-katzenpost-mixserver", Installed: true, Shadow: shadow}}}
	var b bytes.Buffer
	if err := ApplyFixes(context.Background(), &b, track.Env{}, f, false); err == nil {
		t.Fatalf("a failed restart was not an error:\n%s", b.String())
	}
	want := "mv --no-clobber " + shadow + supersededSuffix + " " + shadow
	found := false
	for _, c := range *ran {
		found = found || c == want
	}
	if !found {
		t.Errorf("the moved file was not put back (%q); ran %q", want, *ran)
	}
}

func TestOnlyAnAccountThatExistsIsAlreadyPresent(t *testing.T) {
	for _, c := range []struct {
		code    int
		wantErr bool
	}{{9, false}, {4, true}} {
		fakeRoot(t, func(argv []string) error {
			if argv[0] == "groupadd" {
				return exitWith(t, c.code)
			}
			return nil
		})
		f := hostFacts{Faults: []status.UserFault{oneeyedmanFault()}}
		var b bytes.Buffer
		err := ApplyFixes(context.Background(), &b, track.Env{}, f, false)
		if (err != nil) != c.wantErr {
			t.Errorf("groupadd exit %d: err = %v, want error %v:\n%s", c.code, err, c.wantErr, b.String())
		}
		if c.wantErr && strings.Contains(b.String(), "already present") {
			t.Errorf("groupadd exit %d is reported as an account that is there:\n%s", c.code, b.String())
		}
	}
}

func fakeShow(t *testing.T, props map[string]string) {
	t.Helper()
	old := showUnit
	showUnit = func(_, prop string) string { return props[prop] }
	t.Cleanup(func() { showUnit = old })
}

func packagedDir(t *testing.T, units ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, u := range units {
		if err := os.WriteFile(filepath.Join(dir, u+".service"), []byte("[Unit]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := packagedDirs
	packagedDirs = []string{dir}
	t.Cleanup(func() { packagedDirs = old })
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "dpkg-query"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
}

func TestAMaskIsNotAnOverride(t *testing.T) {
	const unit, path = "pq-katzenpost-mixserver", "/etc/systemd/system/pq-katzenpost-mixserver.service"
	packagedDir(t, unit)
	fakeShow(t, map[string]string{"LoadState": "loaded", "FragmentPath": path})
	if got := shadowed(unit); got != path {
		t.Fatalf("shadowed = %q, want %q: the test does not reach the check", got, path)
	}
	fakeShow(t, map[string]string{"LoadState": "masked", "FragmentPath": path})
	if got := shadowed(unit); got != "" {
		t.Errorf("shadowed = %q: --fix would move the mask away and start the unit", got)
	}
	out, failed := report(t, hostFacts{Systemd: true, UpdaterUser: "kno", Units: []unitReport{{
		Service: "reed-pq-mixserver", Unit: unit, Installed: true, Masked: true,
		State: verify.UnitState{Unit: unit, Active: "inactive", Restarts: -1},
	}}})
	if !failed || !strings.Contains(out, "is masked") || !strings.Contains(out, "systemctl unmask") {
		t.Errorf("a masked unit is not reported as masked:\n%s", out)
	}
}

func TestAUnitWithNoPackagedFileIsNotAnOverride(t *testing.T) {
	const unit, path = "pq-katzenpost-mixserver", "/etc/systemd/system/pq-katzenpost-mixserver.service"
	packagedDir(t)
	fakeShow(t, map[string]string{"LoadState": "loaded", "FragmentPath": path})
	if got := shadowed(unit); got != "" {
		t.Errorf("shadowed = %q: --fix would move the only unit file away", got)
	}
}
