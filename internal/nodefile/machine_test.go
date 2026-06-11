package nodefile

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func machines(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, MachinesDir), 0o755); err != nil {
		t.Fatal(err)
	}
	for n, b := range files {
		if err := os.WriteFile(filepath.Join(dir, MachinesDir, n), []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestEachMachineFileHoldsItsNodesData(t *testing.T) {
	dir := machines(t, map[string]string{
		"luscus.toml":     "operator = \"luscus\"\narch = \"amd64\"\nlan = \"luscus-oneeyedman\"\n",
		"oneeyedman.toml": "operator = \"luscus\"\narch = \"arm64\"\nos = \"debian-13\"\nlan = \"luscus-oneeyedman\"\n",
		"mixy.toml":       "operator = \"annares\"\narch = \"amd64\"\n",
		"README":          "not a machine",
	})
	ms, err := Machines(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 3 || ms["oneeyedman"].Arch != "arm64" || ms["oneeyedman"].OS != "debian-13" {
		t.Fatalf("machines = %+v", ms)
	}
	if got := Operators(ms); !reflect.DeepEqual(got, map[string][]string{
		"luscus": {"luscus", "oneeyedman"}, "annares": {"mixy"}}) {
		t.Errorf("operators = %v", got)
	}
	if got := LANGroups(ms); !reflect.DeepEqual(got, [][]string{{"luscus", "oneeyedman"}}) {
		t.Errorf("lan groups = %v", got)
	}
}

func TestAMachineFileWithAnUnknownKeyIsRefused(t *testing.T) {
	_, err := Machines(machines(t, map[string]string{"mixy.toml": "arhc = \"amd64\"\n"}))
	if err == nil || !strings.Contains(err.Error(), "unknown key arhc") {
		t.Errorf("err = %v, want a refusal that names the key", err)
	}
}

func TestNoMachinesDirectoryIsNoMachines(t *testing.T) {
	ms, err := Machines(t.TempDir())
	if err != nil || len(ms) != 0 {
		t.Errorf("machines = %v, err = %v", ms, err)
	}
}
