package render

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/testutil"
)

func TestWindfallServicePluginsSurviveRendering(t *testing.T) {
	network := knopath.NetworkDir(testutil.RepoRoot(t), "namenlos")
	var src nodeT
	if _, err := toml.DecodeFile(filepath.Join(knopath.EditableIn(network), "nodes/windfallservice-servicenode.toml"), &src); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err := Render(network, out); err != nil {
		t.Fatal(err)
	}
	var got nodeT
	if _, err := toml.DecodeFile(filepath.Join(out, "windfall-pq-servicenode.toml"), &got); err != nil {
		t.Fatal(err)
	}
	plugins := got.ServiceNode.CBORPluginKaetzchen
	if len(plugins) != 3 || plugins[0].Capability != "courier" {
		t.Fatalf("courier and both pigeonservice plugins must be enabled: %+v", plugins)
	}
	for i, cap := range []string{"voucher_router", "service_directory"} {
		p := plugins[i+1]
		if p.Capability != cap || p.Endpoint != cap || p.Disable || p.MaxConcurrency != 1 {
			t.Fatalf("unexpected plugin settings: %+v", p)
		}
		if p.Config["thin_config"] != "/etc/katzenpost/thinclient.toml" {
			t.Fatalf("plugin %s must use the thin client config that the katzenpost package ships", cap)
		}
		if p.Config != nil && p.Config["data_dir"] == "" {
			t.Fatalf("plugin %s lost its data_dir", cap)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "windfall-pq-servicenode.toml")); err != nil {
		t.Fatal(err)
	}
}
