package render

import (
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestRenderedStringFieldsPreserveQuotedPaths(t *testing.T) {
	net := fixture(t)
	node := fixtureNode{"service", "servicenode", "service-pq-servicenode"}
	nodes := append(append([]fixtureNode{}, fixtureNodes...), node)
	put(t, filepath.Join(net, "editable-configs", "nodes", "service-servicenode.toml"), nodeBody(node)+"dataDir = \"/var/lib/quoted\\\"directory\"\n")
	put(t, filepath.Join(net, "editable-configs", "manifest.toml"), manifest(nodes, twoLayers))
	out := t.TempDir()
	if err := Render(net, out); err != nil {
		t.Fatal(err)
	}
	var config struct {
		Server struct{ DataDir string }
	}
	if _, err := toml.DecodeFile(filepath.Join(out, "service-pq-servicenode.toml"), &config); err != nil {
		t.Fatalf("rendered field is not valid TOML: %v", err)
	}
	if config.Server.DataDir != "/var/lib/quoted\"directory" {
		t.Fatalf("directory changed to %q", config.Server.DataDir)
	}
}
