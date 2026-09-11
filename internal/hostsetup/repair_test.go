package hostsetup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/schedule"
)

func brokenHost(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	staleRoot = root
	t.Cleanup(func() { staleRoot = "/" })

	mk := func(p, body string, mode os.FileMode) {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	mk("/usr/local/bin/kno-update", "#!/bin/sh\n. /usr/local/lib/kno/common.sh\n", 0o755)
	mk("/usr/local/bin/kno-push", "#!/bin/sh\n", 0o755)
	mk("/usr/local/lib/kno/common.sh", "# the shared library\n", 0o644)
	mk(schedule.CronFile, "# kno-update periodic check\n*/30 * * * * kno /usr/local/bin/kno-update >/dev/null 2>&1\n", 0o644)
	mk(ServicePath, "[Service]\nExecStart=/usr/local/bin/kno-update\n", 0o644)
	return root
}

func repairLines(t *testing.T) []string {
	t.Helper()
	o := DefaultBootstrapOptions("")
	o.PackageInstall = true
	return RepairSteps(o).Lines()
}

func TestRepairRemovesSupersededTools(t *testing.T) {
	brokenHost(t)
	got := strings.Join(repairLines(t), "\n")
	for _, want := range []string{"kno-update", "kno-push", "common.sh"} {
		if !strings.Contains(got, want) {
			t.Errorf("repair leaves %s behind:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "rm -f") {
		t.Errorf("no removal planned:\n%s", got)
	}
}

func TestRepairRewritesAStaleCronLine(t *testing.T) {
	root := brokenHost(t)
	got := strings.Join(repairLines(t), "\n")
	if !strings.Contains(got, filepath.Join(root, schedule.CronFile)) {
		t.Fatalf("the stale cron fragment is not repaired:\n%s", got)
	}

	o := DefaultBootstrapOptions("")
	s := repairCron(o)
	if s == nil || s.File == nil {
		t.Fatal("no cron repair step")
	}
	if !strings.Contains(s.File.Content, "/usr/bin/kno update") {
		t.Errorf("the new cron line does not call 'kno update':\n%s", s.File.Content)
	}
	if strings.Contains(s.File.Content, "kno-update >") {
		t.Errorf("the new cron line still calls the deleted script:\n%s", s.File.Content)
	}
}

func TestRepairLeavesACorrectCronLineAlone(t *testing.T) {
	root := t.TempDir()
	staleRoot = root
	t.Cleanup(func() { staleRoot = "/" })
	o := DefaultBootstrapOptions("")
	p := filepath.Join(root, schedule.CronFile)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	body := schedule.CronHeader + "\n" + schedule.CronLine(o.User) + "\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if s := repairCron(o); s != nil {
		t.Errorf("a correct cron fragment was rewritten: %s", s)
	}
}

func TestRepairPlansNothingOnACleanHost(t *testing.T) {
	staleRoot = t.TempDir()
	t.Cleanup(func() { staleRoot = "/" })
	if got := repairLines(t); len(got) != 0 {
		t.Errorf("a clean host planned repairs: %v", got)
	}
}

func TestRepairRemovesTheLibDirWithRmdir(t *testing.T) {
	root := t.TempDir()
	staleRoot = root
	t.Cleanup(func() { staleRoot = "/" })
	if err := os.MkdirAll(filepath.Join(root, "usr/local/lib/kno"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(repairLines(t), "\n")
	if !strings.Contains(got, "rmdir") {
		t.Errorf("the lib dir is not removed:\n%s", got)
	}
	if strings.Contains(got, "rm -rf") {
		t.Errorf("repair uses rm -rf, which could take files we did not place:\n%s", got)
	}
}

func TestRepairComesFirstInTheBootstrapPlan(t *testing.T) {
	brokenHost(t)
	o := DefaultBootstrapOptions("")
	o.PackageInstall = true
	p, err := BootstrapPlan(o, Facts{HasSystemd: true})
	if err != nil {
		t.Fatal(err)
	}
	lines := p.Lines()
	if len(lines) == 0 {
		t.Fatal("empty plan")
	}
	if !strings.Contains(lines[0], "rm -f") {
		t.Errorf("the plan does not repair first:\n%s", strings.Join(lines, "\n"))
	}
}

func TestStaleOnHostReportsWhatNeedsRepair(t *testing.T) {
	brokenHost(t)
	got := StaleOnHost("kno")
	joined := strings.Join(got, " ")
	for _, want := range []string{"kno-update", "common.sh", schedule.CronFile} {
		if !strings.Contains(joined, want) {
			t.Errorf("StaleOnHost misses %s: %v", want, got)
		}
	}

	staleRoot = t.TempDir()
	if got := StaleOnHost("kno"); len(got) != 0 {
		t.Errorf("a clean host reports stale artifacts: %v", got)
	}
}

func TestStaleListCoversEveryRetiredTool(t *testing.T) {
	joined := strings.Join(staleFiles, " ")
	for _, tool := range []string{
		"kno-activate", "kno-backup-keys", "kno-bootstrap", "kno-build", "kno-deps",
		"kno-pkg-binary", "kno-pkg-config", "kno-pkg-freebsd", "kno-probe", "kno-push",
		"kno-schedule", "kno-stage", "kno-status", "kno-topology-propose",
		"kno-update", "kno-verify",
	} {
		if !strings.Contains(joined, "/"+tool) {
			t.Errorf("%s is not in the stale list; it would stay on PATH forever", tool)
		}
	}
}

func TestRepairRewritesAStaleUnitWithoutSystemd(t *testing.T) {
	root := brokenHost(t)
	o := DefaultBootstrapOptions("")
	s := repairUnit(o)
	if s == nil || s.File == nil {
		t.Fatal("a stale unit was not repaired")
	}
	if s.File.Path != filepath.Join(root, ServicePath) {
		t.Errorf("repairing the wrong path: %s", s.File.Path)
	}
	if !strings.Contains(s.File.Content, "ExecStart=/usr/bin/kno update") {
		t.Errorf("the new unit does not call 'kno update':\n%s", s.File.Content)
	}
	if strings.Contains(s.File.Content, "bin/kno-update") {
		t.Errorf("the new unit still names the deleted script:\n%s", s.File.Content)
	}

	o.PackageInstall = true
	p, err := BootstrapPlan(o, Facts{HasSystemd: false})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(p.Lines(), "\n"), ServicePath) {
		t.Errorf("the unit repair is skipped without systemd:\n%s", strings.Join(p.Lines(), "\n"))
	}
}

func TestRepairLeavesACorrectUnitAlone(t *testing.T) {
	root := t.TempDir()
	staleRoot = root
	t.Cleanup(func() { staleRoot = "/" })
	o := DefaultBootstrapOptions("")
	p := filepath.Join(root, ServicePath)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(updateService(o.User, o.BinDir)), 0o644); err != nil {
		t.Fatal(err)
	}
	if s := repairUnit(o); s != nil {
		t.Errorf("a correct unit was rewritten: %s", s)
	}
}
