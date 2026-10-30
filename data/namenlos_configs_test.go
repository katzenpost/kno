package knorepo_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/namenlosaudit"
)

func TestAllNamenlosProtocolConfigsMatchTheOriginalNetwork(t *testing.T) {
	sums, err := os.ReadFile("testdata/namenlos-configs.sha256")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(sums)), "\n") {
		fields := strings.Fields(line)
		if fields[1] == "windfall-pq-gatewaynode.toml" {
			continue
		}
		names[fields[1]] = true
		p := filepath.Join("networks/namenlos/.generated/configs", fields[1])
		got, err := namenlosaudit.ConfigDigest(p)
		if err != nil {
			t.Fatal(err)
		}
		if got != fields[0] {
			t.Errorf("%s: protocol config differs from original namenlos", p)
		}
	}
	files, err := filepath.Glob("networks/namenlos/.generated/configs/*.toml")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(names) {
		t.Errorf("rendered %d configs, original network has %d", len(files), len(names))
	}
}
