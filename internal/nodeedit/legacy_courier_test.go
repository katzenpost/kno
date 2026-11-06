package nodeedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/katzenpost/kno/internal/render"
	"github.com/katzenpost/kno/internal/testutil"
)

func TestCurrentLegacyCouriersCanBeListedAndEdited(t *testing.T) {
	repo := testutil.RepoRoot(t)
	for _, file := range []string{"annares-servicenode.toml", "waulandservice-servicenode.toml", "windfallservice-servicenode.toml"} {
		t.Run(file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(repo, "networks", "namenlos", "editable-configs", "nodes", file))
			if err != nil {
				t.Fatal(err)
			}
			body := string(raw)
			var legacy struct {
				Courier                                             bool
				CourierCommand, CourierConfig, CourierLinkPublicKey string
			}
			if _, err := toml.Decode(body, &legacy); err != nil || !legacy.Courier {
				t.Fatalf("actual node is not a legacy courier: %v", err)
			}
			t.Run("list", func(t *testing.T) {
				listed, err := ListServicePlugins(body)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, plugin := range listed {
					found = found || plugin.ID == "courier" && plugin.Enabled
				}
				normalized, changed, err := normalizeLegacyCourier(body)
				if err != nil || !changed || !found {
					t.Fatalf("existing legacy courier was omitted: %v", err)
				}
				semantic, err := decodeSemanticPluginDocument(normalized)
				if err != nil || len(semantic.ServiceNode.CBORPluginKaetzchen) != 1 {
					t.Fatalf("normalized courier: %v", err)
				}
				courier := semantic.ServiceNode.CBORPluginKaetzchen[0]
				key, _ := nestedPluginString(courier.PKIAdvertizedData, "courier", "linkPublicKey")
				if courier.Command != legacy.CourierCommand || courier.Config["c"] != legacy.CourierConfig || key != legacy.CourierLinkPublicKey {
					t.Fatal("legacy courier command, config or public key was not carried over")
				}
			})
			t.Run("update", func(t *testing.T) {
				edit := PluginEdit{ID: "courier", Action: PluginUpdate, MaxConcurrency: 2, PKIAdvertizedData: map[string]any{"courier": map[string]any{"linkPublicKey": legacy.CourierLinkPublicKey}}}
				plan, err := PlanServicePlugin(body, edit)
				if err != nil || !plan.Changed {
					t.Fatalf("legacy courier update failed: %v", err)
				}
				if !strings.Contains(plan.Body, "MaxConcurrency = 2") || !strings.Contains(plan.Body, "kaetzchen = [\"echo\"]") {
					t.Fatal("update lost existing echo or did not update courier")
				}
				if !strings.HasPrefix(plan.Body, strings.Replace(body, "courier = true", "courier = false", 1)) {
					t.Fatal("courier adaptation changed unrelated source bytes")
				}
				for _, line := range strings.Split(body, "\n") {
					if strings.HasPrefix(strings.TrimSpace(line), "#") && !strings.Contains(plan.Body, line) {
						t.Fatal("update lost an unrelated existing config comment")
					}
				}
			})
			t.Run("disable-enable-remove", func(t *testing.T) {
				disabled, err := PlanServicePlugin(body, PluginEdit{ID: "courier", Action: PluginDisable})
				if err != nil {
					t.Fatalf("legacy courier disable failed: %v", err)
				}
				enabled, err := PlanServicePlugin(disabled.Body, PluginEdit{ID: "courier", Action: PluginEnable})
				if err != nil {
					t.Fatalf("legacy courier re-enable failed: %v", err)
				}
				listed, err := ListServicePlugins(enabled.Body)
				if err != nil {
					t.Fatal(err)
				}
				count := 0
				for _, plugin := range listed {
					if plugin.ID == "courier" && plugin.Enabled {
						count++
					}
				}
				if count != 1 {
					t.Fatalf("re-enable produced %d enabled couriers", count)
				}
				assertLegacyCourierRendered(t, file, disabled.Body, 1, 0)
				assertLegacyCourierRendered(t, file, enabled.Body, 1, 1)
				removed, err := PlanServicePlugin(body, PluginEdit{ID: "courier", Action: PluginRemove})
				if err != nil {
					t.Fatalf("legacy courier removal failed: %v", err)
				}
				listed, err = ListServicePlugins(removed.Body)
				if err != nil {
					t.Fatal(err)
				}
				for _, plugin := range listed {
					if plugin.ID == "courier" {
						t.Fatal("removed legacy courier is still configured")
					}
				}
				assertLegacyCourierRendered(t, file, removed.Body, 0, 0)
			})
			t.Run("unrelated-motd", func(t *testing.T) {
				var manifest lifecycleManifest
				if _, err := toml.DecodeFile(filepath.Join(repo, "networks", "namenlos", "editable-configs", "manifest.toml"), &manifest); err != nil {
					t.Fatal(err)
				}
				edit := PluginEdit{Action: PluginAdd, ID: "motd", Config: map[string]any{"Text": "maintenance"}, UserForwardPayloadLength: manifest.Network.Sphinx.UserForwardPayloadLength}
				plan, err := PlanServicePlugin(body, edit)
				if err != nil || !strings.HasPrefix(plan.Body, body) {
					t.Fatalf("unrelated plugin edit migrated legacy courier fields: %v", err)
				}
			})
		})
	}
}

func assertLegacyCourierRendered(t *testing.T, file, body string, total, enabled int) {
	t.Helper()
	repo := lifecycleRepo(t)
	netDir := filepath.Join(repo, "networks", "namenlos")
	if err := os.WriteFile(filepath.Join(netDir, "editable-configs", "nodes", file), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var source struct {
		ConfigName string
		Courier    bool
	}
	if _, err := toml.Decode(body, &source); err != nil || source.Courier {
		t.Fatalf("legacy courier was not disabled during selected conversion: %v", err)
	}
	out := filepath.Join(t.TempDir(), "configs")
	if err := render.Render(netDir, out); err != nil {
		t.Fatal(err)
	}
	var config struct {
		ServiceNode struct {
			Plugins []struct {
				Capability string
				Disable    bool
			} `toml:"CBORPluginKaetzchen"`
		}
	}
	if _, err := toml.DecodeFile(filepath.Join(out, source.ConfigName+".toml"), &config); err != nil {
		t.Fatal(err)
	}
	count, active := 0, 0
	for _, plugin := range config.ServiceNode.Plugins {
		if plugin.Capability == "courier" {
			count++
			if !plugin.Disable {
				active++
			}
		}
	}
	if count != total || active != enabled {
		t.Fatalf("rendered courier semantics: total=%d enabled=%d; want %d/%d", count, active, total, enabled)
	}
}
