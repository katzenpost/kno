package activate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKnoInstallsOnlyKnoPackages(t *testing.T) {
	dir := t.TempDir()
	kno := filepath.Join(dir, "kno_0.0.104_amd64.deb")
	hc := filepath.Join(dir, "kno-host-config_0.0.104_all.deb")
	kp := filepath.Join(dir, "katzenpost_0.0.104_amd64.deb")
	for _, p := range []string{kno, hc, kp} {
		mkdeb(t, p, nil)
	}
	if _, err := knoInstallPlan([]string{kno, hc}, false); err != nil {
		t.Errorf("kno and kno-host-config were refused: %v", err)
	}
	if _, err := knoInstallPlan([]string{kno, kp}, false); err == nil {
		t.Error("--kno installed a package that is not kno")
	}
}

func TestKnoInstallsFromItsOwnCopy(t *testing.T) {
	old := signaturesRequired
	signaturesRequired = func() bool { return false }
	defer func() { signaturesRequired = old }()
	dir := t.TempDir()
	kno := filepath.Join(dir, "kno_0.0.104_amd64.deb")
	mkdeb(t, kno, nil)
	var out strings.Builder
	if rc := runKno(&out, []string{kno}, true); rc != 0 {
		t.Fatalf("rc %d:\n%s", rc, out.String())
	}
	if strings.Contains(out.String(), dir) || !strings.Contains(out.String(), "kno-activate-") {
		t.Errorf("dpkg reads the staged file, not a copy that only root can change:\n%s", out.String())
	}
	if err := os.Symlink(kno, filepath.Join(dir, "link.deb")); err != nil {
		t.Fatal(err)
	}
	if rc := runKno(&out, []string{filepath.Join(dir, "link.deb")}, true); rc == 0 {
		t.Error("--kno installed through a symlink")
	}
}

func TestRestartRunsAfterTheInstallAndOnlyWarns(t *testing.T) {
	steps := restartSteps([]string{"kpserver@mixy"})
	if len(steps) != 2 || steps[1].Argv[1] != "restart" || steps[1].Argv[3] != "kpserver@mixy" || steps[1].Kind != StepRunIgnore || !steps[1].Warn {
		t.Errorf("restart steps = %+v", steps)
	}
	var out strings.Builder
	p := Plan{Steps: []Step{{Kind: StepRunIgnore, Argv: []string{"false"}, Warn: true}}}
	if err := Apply(t.Context(), p, Options{}, &out); err != nil {
		t.Errorf("a failed restart failed the install: %v", err)
	}
	if !strings.Contains(out.String(), "WARN") {
		t.Errorf("a failed restart was not reported:\n%s", out.String())
	}
}

func TestAFreeBSDKnoPackageInstallsWithPkgAdd(t *testing.T) {
	p, err := knoInstallPlan([]string{"/r/kno-0.0.107.20261026.g1_1.pkg"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(p.Steps[0].Argv, " "); got != "env ASSUME_ALWAYS_YES=yes pkg add -f /r/kno-0.0.107.20261026.g1_1.pkg" {
		t.Errorf("argv = %q", got)
	}
	if _, err := knoInstallPlan([]string{"/r/katzenpost-0.0.107.pkg"}, false); err == nil {
		t.Error("a katzenpost package went through kno activate --kno")
	}
}
