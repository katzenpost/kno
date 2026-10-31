package hostconfig

import (
	"github.com/katzenpost/kno/internal/config"
	"testing"
)

func TestSelectedHostKeepsEffectiveOperatorSettings(t *testing.T) {
	c := config.Config{Network: "namenlos", Hosts: map[string]config.Host{"node": {Services: []string{"node-pq-servicenode"}}}}
	c.Defaults.Privilege = "su"
	c.Defaults.Scheduler = "none"
	c.Defaults.Kpclientd = config.Bool(false)
	c.Defaults.Install = "pkg"
	c.Update.Policy = "off"
	selected := EffectiveHost(c, "node", "")
	if selected.Privilege("node") != "su" || selected.Scheduler("node") != "none" || selected.RunsKpclientd("node") || selected.Update.Policy != "off" || selected.InstallMethod("node") != "pkg" {
		t.Fatalf("settings changed: %+v", selected)
	}
	if c.Hosts["node"].Privilege != "" {
		t.Fatal("source operator config changed")
	}
}
