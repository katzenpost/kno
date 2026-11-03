package render

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestRenderPreservesCompleteServicePluginTables(t *testing.T) {
	net := fixture(t)
	node := fixtureNode{"service", "servicenode", "service-pq-servicenode"}
	nodes := append(append([]fixtureNode{}, fixtureNodes...), node)
	plugins := `
[[ServiceNode.Kaetzchen]]
Capability = "motd"
Endpoint = "+motd"
Disable = false
[ServiceNode.Kaetzchen.Config]
Text = "Hello \"operator\"\nSecond line"

[[ServiceNode.CBORPluginKaetzchen]]
Capability = "future-plugin"
Endpoint = "+future"
Command = "/usr/bin/future-plugin"
MaxConcurrency = 1
Disable = true
FutureSetting = "keep this"
[ServiceNode.CBORPluginKaetzchen.Config]
path = "/var/lib/quoted\"path"
limit = 3
[ServiceNode.CBORPluginKaetzchen.PKIAdvertizedData.future]
publicReference = "public-reference"
`
	body := nodeBody(node) + plugins
	put(t, filepath.Join(net, "editable-configs", "nodes", "service-servicenode.toml"), body)
	put(t, filepath.Join(net, "editable-configs", "manifest.toml"), manifest(nodes, twoLayers))
	out := t.TempDir()
	if err := Render(net, out); err != nil {
		t.Fatal(err)
	}
	var before, after map[string]any
	if _, err := toml.Decode(body, &before); err != nil {
		t.Fatal(err)
	}
	rendered, err := os.ReadFile(filepath.Join(out, "service-pq-servicenode.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := toml.Decode(string(rendered), &after); err != nil {
		t.Fatalf("rendered plugin TOML is invalid: %v", err)
	}
	if !reflect.DeepEqual(before["ServiceNode"], after["ServiceNode"]) {
		t.Fatalf("plugin settings changed: got %#v, want %#v", after["ServiceNode"], before["ServiceNode"])
	}
}
