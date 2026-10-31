package render

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/testutil"
)

func TestWindfallServicePluginsSurviveRendering(t *testing.T) {
	network := knopath.NetworkDir(testutil.RepoRoot(t), "namenlos")
	path := filepath.Join(knopath.EditableIn(network), "nodes/windfallservice-servicenode.toml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	commented := commentedPlugins(string(body))
	if commented == "" {
		t.Fatal("windfall lost its commented plugin configuration")
	}
	var lines []string
	for _, line := range strings.Split(commented, "\n") {
		lines = append(lines, strings.TrimPrefix(strings.TrimPrefix(line, "#"), " "))
	}
	uncommented := strings.Join(lines, "\n")
	var src nodeT
	if _, err := toml.Decode(uncommented, &src); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err := Render(network, out); err != nil {
		t.Fatal(err)
	}
	defaultConfig, err := os.ReadFile(filepath.Join(out, "windfall-pq-servicenode.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(defaultConfig), commented) {
		t.Fatal("rendering dropped the commented plugin blocks")
	}
	var active nodeT
	if _, err := toml.Decode(string(defaultConfig), &active); err != nil {
		t.Fatal(err)
	}
	if len(active.ServiceNode.CBORPluginKaetzchen) != 1 || active.ServiceNode.CBORPluginKaetzchen[0].Capability != "courier" {
		t.Fatal("unavailable external plugins are active, or the courier was disabled")
	}
	copy := t.TempDir()
	if err := fsutil.CopyTreeGuarded(knopath.EditableIn(network), knopath.EditableIn(copy)); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(knopath.EditableIn(copy), "nodes/windfallservice-servicenode.toml")
	if err := os.WriteFile(path, []byte(strings.Replace(string(body), commented, uncommented, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Render(copy, out); err != nil {
		t.Fatal(err)
	}
	var got struct {
		ServiceNode struct {
			CBORPluginKaetzchen []cborPluginT
		}
	}
	if _, err := toml.DecodeFile(filepath.Join(out, "windfall-pq-servicenode.toml"), &got); err != nil {
		t.Fatal(err)
	}
	plugins := got.ServiceNode.CBORPluginKaetzchen
	if len(plugins) != 3 || plugins[0].Capability != "courier" {
		t.Fatalf("courier and both custom plugins must remain enabled: %+v", plugins)
	}
	if !reflect.DeepEqual(plugins[1:], src.ServiceNode.CBORPluginKaetzchen) {
		t.Fatalf("custom plugin fields changed during rendering: %+v", plugins[1:])
	}
	for i, cap := range []string{"voucher_router", "service_directory"} {
		p := plugins[i+1]
		if p.Capability != cap || p.Endpoint != cap || p.Disable || p.MaxConcurrency != 1 {
			t.Fatalf("unexpected plugin settings: %+v", p)
		}
		if p.Config["thin_config"] != "/etc/katzenpost-servicenode/pigeonservice-thinclient.toml" {
			t.Fatalf("plugin %s lost its thin-client path", cap)
		}
	}
}
