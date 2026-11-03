package nodeedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestPluginHeaderSyntaxMatrixCannotDuplicateCapabilityInCandidate(t *testing.T) {
	for _, variant := range []struct {
		name string
		text string
	}{
		{name: "comment", text: "[[ServiceNode.CBORPluginKaetzchen]] # ordinary TOML comment\nCapability = \"echo\"\nCommand = \"/usr/bin/echo-plugin\"\nMaxConcurrency = 1\n"},
		{name: "spacing", text: "[[ ServiceNode . CBORPluginKaetzchen ]]\nCapability = \"echo\"\nCommand = \"/usr/bin/echo-plugin\"\nMaxConcurrency = 1\n"},
		{name: "quoted", text: "[[\"ServiceNode\".\"CBORPluginKaetzchen\"]]\nCapability = \"echo\"\nCommand = \"/usr/bin/echo-plugin\"\nMaxConcurrency = 1\n"},
		{name: "lowercase", text: "[[servicenode.cborpluginkaetzchen]]\ncapability = \"echo\"\ncommand = \"/usr/bin/echo-plugin\"\nmaxconcurrency = 1\n"},
		{name: "inline-table", text: "ServiceNode.CBORPluginKaetzchen = [{ Capability = \"echo\", Command = \"/usr/bin/echo-plugin\", MaxConcurrency = 1 }]\n"},
	} {
		t.Run(variant.name, func(t *testing.T) {
			repo := lifecycleRepo(t)
			nodePath := filepath.Join(repo, "networks/namenlos/editable-configs/nodes/lilly-servicenode.toml")
			body, err := os.ReadFile(nodePath)
			if err != nil {
				t.Fatal(err)
			}
			updated := string(body) + "\n" + variant.text
			if variant.name == "inline-table" {
				updated = variant.text + string(body)
			}
			if _, err := toml.Decode(updated, &map[string]any{}); err != nil {
				t.Fatalf("header fixture is not valid TOML: %v", err)
			}
			plan, err := PlanCandidate(repo, "namenlos", []CandidateEdit{{Path: "nodes/lilly-servicenode.toml", Base: string(body), Text: updated}})
			if err != nil {
				t.Fatal(err)
			}
			err = ApplyLifecycle(plan)
			if err != nil {
				if !strings.Contains(strings.ToLower(err.Error()), "capability") && !strings.Contains(strings.ToLower(err.Error()), "plugin") {
					t.Fatalf("candidate failed for an unrelated reason: %v", err)
				}
				return
			}
			rendered, err := os.ReadFile(filepath.Join(repo, "networks/namenlos/.generated/configs/lilly-pq-servicenode.toml"))
			if err != nil {
				t.Fatal(err)
			}
			if countCapability(string(rendered), "echo") > 1 {
				t.Fatalf("candidate published duplicate echo capabilities using %s syntax", variant.name)
			}
		})
	}
}

func TestUppercaseLegacyCapabilityCannotBypassDuplicateCheck(t *testing.T) {
	repo := lifecycleRepo(t)
	nodePath := filepath.Join(repo, "networks/namenlos/editable-configs/nodes/lilly-servicenode.toml")
	body, err := os.ReadFile(nodePath)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(body), "kaetzchen = [\"echo\"]", "Kaetzchen = [\"echo\"]", 1)
	updated += "\n[[ServiceNode.CBORPluginKaetzchen]]\nCapability = \"echo\"\nCommand = \"/usr/bin/echo-plugin\"\nMaxConcurrency = 1\n"
	plan, err := PlanCandidate(repo, "namenlos", []CandidateEdit{{Path: "nodes/lilly-servicenode.toml", Base: string(body), Text: updated}})
	if err != nil {
		t.Fatal(err)
	}
	err = ApplyLifecycle(plan)
	if err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "capability") {
			t.Fatalf("candidate failed for an unrelated reason: %v", err)
		}
		return
	}
	t.Fatal("candidate accepted duplicate echo capabilities using an uppercase legacy field")
}

func advertisedCourierKey(body string) string {
	var doc struct {
		ServiceNode struct {
			CBORPluginKaetzchen []struct {
				Capability        string `toml:"Capability"`
				PKIAdvertizedData struct {
					Courier struct {
						LinkPublicKey string `toml:"linkPublicKey"`
					} `toml:"courier"`
				} `toml:"PKIAdvertizedData"`
			} `toml:"CBORPluginKaetzchen"`
		} `toml:"ServiceNode"`
	}
	if _, err := toml.Decode(body, &doc); err != nil {
		return "invalid TOML"
	}
	for _, plugin := range doc.ServiceNode.CBORPluginKaetzchen {
		if plugin.Capability != "courier" {
			continue
		}
		return plugin.PKIAdvertizedData.Courier.LinkPublicKey
	}
	return ""
}

func countCapability(body, capability string) int {
	var doc struct {
		ServiceNode struct {
			Kaetzchen []struct {
				Capability string `toml:"Capability"`
				Disable    bool   `toml:"Disable"`
			} `toml:"Kaetzchen"`
			CBORPluginKaetzchen []struct {
				Capability string `toml:"Capability"`
				Disable    bool   `toml:"Disable"`
			} `toml:"CBORPluginKaetzchen"`
		} `toml:"ServiceNode"`
	}
	if _, err := toml.Decode(body, &doc); err != nil {
		return -1
	}
	count := 0
	for _, plugin := range doc.ServiceNode.Kaetzchen {
		if plugin.Capability == capability && !plugin.Disable {
			count++
		}
	}
	for _, plugin := range doc.ServiceNode.CBORPluginKaetzchen {
		if plugin.Capability == capability && !plugin.Disable {
			count++
		}
	}
	return count
}
