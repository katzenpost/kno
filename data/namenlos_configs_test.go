package knorepo_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func namenlosConfigDigest(t *testing.T, path string) string {
	t.Helper()
	var m map[string]any
	if _, err := toml.DecodeFile(path, &m); err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(path)
	drop := func(keys ...string) {
		table := m
		for _, key := range keys[:len(keys)-1] {
			next, ok := table[key].(map[string]any)
			if !ok {
				return
			}
			table = next
		}
		delete(table, keys[len(keys)-1])
	}
	drop("Server", "DataDir")
	drop("DataDir")
	drop("Gateway", "SpoolDB", "Bolt", "SpoolDB")
	if name == "annares-pq-servicenode.toml" || name == "windfall-pq-servicenode.toml" {
		serviceNode := m["ServiceNode"].(map[string]any)
		for _, plugin := range serviceNode["CBORPluginKaetzchen"].([]map[string]any) {
			delete(plugin, "Command")
			delete(plugin["Config"].(map[string]any), "c")
		}
	}
	if strings.Contains(name, "-pq-storagenode") {
		m["MetricsAddress"] = "127.0.0.1:23002"
	}
	if name == "oneeyedman-pq-mixserver.toml" {
		m["Server"].(map[string]any)["BindAddresses"] = []string{"tcp://0.0.0.0:49278"}
	}
	b, err := json.Marshal(normalizeNamenlos(m))
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func normalizeNamenlos(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = normalizeNamenlos(e)
		}
		return x
	case []map[string]any:
		out := make([]any, 0, len(x))
		for _, e := range x {
			if e["Identifier"] == "windfallgw" || e["Name"] == "windfallgw" {
				continue
			}
			out = append(out, normalizeNamenlos(e))
		}
		if len(x) > 0 && x[0]["Identifier"] != nil {
			sort.Slice(out, func(i, j int) bool {
				return out[i].(map[string]any)["Identifier"].(string) < out[j].(map[string]any)["Identifier"].(string)
			})
		}
		return out
	case []any:
		for i, e := range x {
			x[i] = normalizeNamenlos(e)
		}
		return x
	case string:
		if strings.HasPrefix(x, "-----BEGIN ") {
			return strings.TrimSpace(x)
		}
		return x
	default:
		return v
	}
}

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
		if got := namenlosConfigDigest(t, p); got != fields[0] {
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
