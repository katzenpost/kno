package nodeedit

import (
	"os"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestPluginCatalogAndCourierPlanPreserveNodeAndUnknownBlocks(t *testing.T) {
	body := `name = "lilly"
role = "servicenode"
identityPublicKey = "public-value"
kaetzchen = ["echo"]

# future plugin fields stay here
[[ServiceNode.CBORPluginKaetzchen]]
Capability = "future"
Endpoint = "future"
VendorField = "keep-me"
Config = { unknown = "preserve" }
`
	known := map[string]bool{}
	for _, d := range ServicePluginCatalog() {
		known[d.ID] = true
	}
	for _, id := range []string{"echo", "motd", "testdest", "courier", "echo-plugin", "http-proxy-server"} {
		if !known[id] {
			t.Fatalf("catalog is missing %s", id)
		}
	}
	edit := PluginEdit{Action: PluginAdd, ID: "courier", Endpoint: "courier", Command: "/opt/kp/courier", MaxConcurrency: 1,
		Config:            map[string]any{"c": "/etc/kp/courier.toml"},
		PKIAdvertizedData: map[string]any{"courier": map[string]any{"linkPublicKey": "verified-public-key"}}}
	plan, err := PlanServicePlugin(body, edit)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Changed || !strings.Contains(plan.Body, `identityPublicKey = "public-value"`) || !strings.Contains(plan.Body, `VendorField = "keep-me"`) || !strings.Contains(plan.Body, `kaetzchen = ["echo"]`) {
		t.Fatalf("plan did not preserve unrelated source: %s", plan.Body)
	}
	var decoded struct {
		ServiceNode struct {
			CBOR []struct {
				Capability        string
				Endpoint          string
				Config            map[string]string
				PKIAdvertizedData map[string]map[string]string
			} `toml:"CBORPluginKaetzchen"`
		} `toml:"ServiceNode"`
	}
	if _, err := toml.Decode(plan.Body, &decoded); err != nil {
		t.Fatalf("invalid TOML: %v\n%s", err, plan.Body)
	}
	if len(decoded.ServiceNode.CBOR) != 2 || decoded.ServiceNode.CBOR[1].Capability != "courier" || decoded.ServiceNode.CBOR[1].PKIAdvertizedData["courier"]["linkPublicKey"] != "verified-public-key" {
		t.Fatalf("courier values were not rendered: %#v", decoded.ServiceNode.CBOR)
	}
	listed, err := ListServicePlugins(plan.Body)
	if err != nil || len(listed) != 3 {
		t.Fatalf("list = %#v, %v", listed, err)
	}
}

func TestEveryAvailableCatalogPluginCanBeAddedAndListed(t *testing.T) {
	cases := []PluginEdit{
		{Action: PluginAdd, ID: "echo"},
		{Action: PluginAdd, ID: "motd", Config: map[string]any{"Text": "hello"}, UserForwardPayloadLength: 256},
		{Action: PluginAdd, ID: "testdest"},
		{Action: PluginAdd, ID: "courier", Command: "/opt/courier", Endpoint: "courier", MaxConcurrency: 1, Config: map[string]any{"c": "/etc/courier.toml"}, PKIAdvertizedData: map[string]any{"courier": map[string]any{"linkPublicKey": "public-value"}}},
		{Action: PluginAdd, ID: "echo-plugin", Capability: "test-echo", Endpoint: "test-echo", Command: "/opt/echo-plugin", MaxConcurrency: 1, Config: map[string]any{"log_dir": "/var/log/echo"}},
		{Action: PluginAdd, ID: "http-proxy-server", Endpoint: "+http", Command: "/opt/http-proxy-server", MaxConcurrency: 1, Config: map[string]any{"host": "localhost:4242", "log_dir": "/var/log/proxy"}},
	}
	for _, edit := range cases {
		t.Run(edit.ID, func(t *testing.T) {
			plan, err := PlanServicePlugin(`name="node"`, edit)
			if err != nil {
				t.Fatal(err)
			}
			listed, err := ListServicePlugins(plan.Body)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, p := range listed {
				if p.ID == edit.ID {
					found = true
				}
			}
			if !found {
				t.Fatalf("added plugin %s not listed: %#v", edit.ID, listed)
			}
		})
	}
}

func TestPluginActionsPreserveOtherBlocksAndRejectSecretOrBadMotd(t *testing.T) {
	body := `name = "lilly"
[[ServiceNode.CBORPluginKaetzchen]]
Capability = "courier"
Endpoint = "courier"
Command = "/opt/kp/courier"
Disable = false
Config = { c = "/etc/kp/courier.toml", future = "keep" }
`
	plan, err := PlanServicePlugin(body, PluginEdit{Action: PluginDisable, ID: "courier"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Body, "Disable = true") || !strings.Contains(plan.Body, `future = "keep"`) {
		t.Fatalf("disable lost existing config: %s", plan.Body)
	}
	updated, err := PlanServicePlugin(body, PluginEdit{Action: PluginUpdate, ID: "courier", Config: map[string]any{"c": "/etc/new.toml"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(updated.Body, `future = "keep"`) || !strings.Contains(updated.Body, `c = "/etc/new.toml"`) || !strings.Contains(updated.Body, `Command = "/opt/kp/courier"`) {
		t.Fatalf("update did not preserve unrelated fields: %s", updated.Body)
	}
	if _, err := PlanServicePlugin(body, PluginEdit{Action: PluginUpdate, ID: "courier", Command: "/opt/kp/courier", Config: map[string]any{"c": "/tmp/c"}, PKIAdvertizedData: map[string]any{"courier": map[string]any{"linkPublicKey": "-----BEGIN PRIVATE KEY-----x"}}}); err == nil {
		t.Fatal("private key was accepted")
	}
	if _, err := PlanServicePlugin(`name = "x"`, PluginEdit{Action: PluginAdd, ID: "motd", Config: map[string]any{"Text": "bad\u0001"}}); err == nil {
		t.Fatal("nonprintable MOTD accepted")
	}
	if _, err := PlanServicePlugin(`name = "x"`, PluginEdit{Action: PluginAdd, ID: "motd", Config: map[string]any{"Text": strings.Repeat("x", 1025)}}); err == nil {
		t.Fatal("overlength MOTD accepted")
	}
}

func TestCourierPlanPreservesSuppliedPublicPEM(t *testing.T) {
	public, err := os.ReadFile(testPublicKeyFile(t, "KYBER768-X25519"))
	if err != nil {
		t.Fatal(err)
	}
	edit := PluginEdit{Action: PluginAdd, ID: "courier", Command: "/opt/courier", Endpoint: "courier", Config: map[string]any{"c": "/etc/courier.toml"}, PKIAdvertizedData: map[string]any{"courier": map[string]any{"linkPublicKey": string(public)}}}
	plan, err := PlanServicePlugin(`name="n"`, edit)
	if err != nil {
		t.Fatal(err)
	}
	var parsed parsedPluginBlock
	if _, err := toml.Decode(plan.Body, &parsed); err != nil || len(parsed.ServiceNode.CBOR) != 1 {
		t.Fatalf("planned plugin TOML invalid: %v", err)
	}
	got, ok := nestedPluginString(parsed.ServiceNode.CBOR[0], "PKIAdvertizedData", "courier", "linkPublicKey")
	if !ok || got != string(public) {
		t.Fatal("supplied public PEM changed in the plugin candidate")
	}
}

func TestCourierUpdateKeepsAdvertisedKey(t *testing.T) {
	body := `name = "n"
[[ServiceNode.CBORPluginKaetzchen]]
Capability = "courier"
Endpoint = "courier"
Command = "/opt/courier"
Config = { c = "/etc/courier.toml" }
PKIAdvertizedData = { courier = { linkPublicKey = "existing trusted value" } }
`
	edit := PluginEdit{Action: PluginUpdate, ID: "courier", Config: map[string]any{"c": "/etc/new-courier.toml"}, PKIAdvertizedData: map[string]any{"courier": map[string]any{"linkPublicKey": "existing trusted value"}}}
	plan, err := PlanServicePlugin(body, edit)
	if err != nil || !strings.Contains(plan.Body, `c = "/etc/new-courier.toml"`) {
		t.Fatalf("unrelated courier update failed: %v\n%s", err, plan.Body)
	}
}

func TestNestedPluginConfigUpdatesPreserveUnknownValues(t *testing.T) {
	body := `[[ServiceNode.CBORPluginKaetzchen]]
Capability = "echo"
Endpoint = "echo"
Command = "/opt/echo-plugin"
Disable = false
[ServiceNode.CBORPluginKaetzchen.Config]
log_dir = "/var/log/echo"
future = "keep"
`
	plan, err := PlanServicePlugin(body, PluginEdit{Action: PluginUpdate, ID: "echo-plugin", Config: map[string]any{"log_dir": "/var/log/new"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Body, `log_dir = "/var/log/new"`) || !strings.Contains(plan.Body, `future = "keep"`) {
		t.Fatalf("nested config did not preserve fields: %s", plan.Body)
	}
	listed, err := ListServicePlugins(plan.Body)
	if err != nil || len(listed) != 1 || listed[0].ID != "echo-plugin" {
		t.Fatalf("listed plugin = %#v, err=%v", listed, err)
	}
}

func TestPluginEndpointAndExternalConfigValidation(t *testing.T) {
	for _, tc := range []struct {
		endpoint string
		ok       bool
	}{{"+echo", true}, {"a.b_2-", true}, {"../outside", false}, {"A", false}, {strings.Repeat("a", 33), false}} {
		err := validPluginEndpoint(tc.endpoint)
		if (err == nil) != tc.ok {
			t.Fatalf("endpoint %q: err=%v", tc.endpoint, err)
		}
	}
	e := PluginEdit{Action: PluginAdd, ID: "echo-plugin", Endpoint: "echo", Command: "/x/echo-plugin", Config: map[string]any{"log_dir": "/var/log/kp", "nonstring": 1}}
	if _, err := PlanServicePlugin(`name="n"`, e); err == nil {
		t.Fatal("external non-string Config accepted")
	}
}

func TestMotdRequiresKnownGeometryAndValidatesGeometryLimit(t *testing.T) {
	edit := PluginEdit{Action: PluginAdd, ID: "motd", Config: map[string]any{"Text": "hello"}}
	if _, err := PlanServicePlugin(`name="n"`, edit); err == nil {
		t.Fatal("MOTD accepted without payload geometry")
	}
	edit.UserForwardPayloadLength = 4
	if _, err := PlanServicePlugin(`name="n"`, edit); err == nil {
		t.Fatal("MOTD exceeded known payload geometry")
	}
	edit.Config["Text"] = "ok"
	plan, err := PlanServicePlugin(`name="n"`, edit)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Body, `[ServiceNode.Kaetzchen]`) {
		t.Fatalf("MOTD table missing: %s", plan.Body)
	}
}
