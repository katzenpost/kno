package namenlosaudit

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

func ConfigDigest(path string) (string, error) {
	var m map[string]any
	if _, err := toml.DecodeFile(path, &m); err != nil {
		return "", err
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
	drop("BridgeSocket")
	drop("Gateway", "SpoolDB", "Bolt", "SpoolDB")
	if name == "annares-pq-servicenode.toml" || name == "windfall-pq-servicenode.toml" {
		serviceNode, _ := m["ServiceNode"].(map[string]any)
		plugins, _ := serviceNode["CBORPluginKaetzchen"].([]map[string]any)
		for _, plugin := range plugins {
			delete(plugin, "Command")
			config, _ := plugin["Config"].(map[string]any)
			delete(config, "c")
		}
	}
	if strings.Contains(name, "-pq-storagenode") {
		m["MetricsAddress"] = "127.0.0.1:23002"
	}
	if name == "oneeyedman-pq-mixserver.toml" {
		if server, ok := m["Server"].(map[string]any); ok {
			server["BindAddresses"] = []string{"tcp://0.0.0.0:49278"}
		}
	}
	b, err := json.Marshal(normalizeNamenlos(m))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(b)), nil
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
				return fmt.Sprint(out[i].(map[string]any)["Identifier"]) < fmt.Sprint(out[j].(map[string]any)["Identifier"])
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
