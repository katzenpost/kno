package preflight

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAServiceOrUnitNameRunsNothingInTheProbe(t *testing.T) {
	mark := filepath.Join(t.TempDir(), "ran")
	evil := "x$(touch " + mark + ")"
	script := Probe(map[string]string{evil: t.TempDir()}, []string{evil})
	if out, err := exec.Command("sh", "-c", script).CombinedOutput(); err != nil {
		t.Logf("%v: %s", err, out)
	}
	if _, err := os.Stat(mark); err == nil {
		t.Error("a service or unit name ran a command in the preflight probe")
	}
}
