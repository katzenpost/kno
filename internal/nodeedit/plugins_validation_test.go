package nodeedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestPluginAddRejectsCapabilityAlreadyEnabledInOtherKind(t *testing.T) {
	repo := lifecycleRepo(t)
	t.Setenv("KNO_REPO", repo)
	_, err := runEditorCommand(t, "", "plugin", "edit", "lilly", "--network", "namenlos", "--action", "add", "--id", "echo-plugin", "--capability", "echo", "--endpoint", "+echo", "--command", "/usr/bin/echo-plugin", "--config", `{"log_dir":"/var/log/katzenpost"}`, "--write")
	if err != nil {
		if !strings.Contains(err.Error(), `service capability "echo"`) {
			t.Fatalf("plugin edit failed for an unrelated reason: %v", err)
		}
		return
	}
	renderedPath := filepath.Join(repo, "networks", "namenlos", ".generated", "configs", "lilly-pq-servicenode.toml")
	var rendered struct {
		ServiceNode struct {
			Kaetzchen           []struct{ Capability string }
			CBORPluginKaetzchen []struct{ Capability string }
		}
	}
	if _, err := toml.DecodeFile(renderedPath, &rendered); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, plugin := range rendered.ServiceNode.Kaetzchen {
		if plugin.Capability == "echo" {
			count++
		}
	}
	for _, plugin := range rendered.ServiceNode.CBORPluginKaetzchen {
		if plugin.Capability == "echo" {
			count++
		}
	}
	if count > 1 {
		t.Fatalf("published service config contains %d enabled plugins with capability echo", count)
	}
}

func TestNodeCandidateRejectsDuplicateActiveCapabilitiesAcrossKinds(t *testing.T) {
	repo := lifecycleRepo(t)
	nodePath := filepath.Join(repo, "networks", "namenlos", "editable-configs", "nodes", "lilly-servicenode.toml")
	body, err := os.ReadFile(nodePath)
	if err != nil {
		t.Fatal(err)
	}
	updated := string(body) + "\n[[ServiceNode.CBORPluginKaetzchen]]\nCapability = \"echo\"\nCommand = \"/usr/bin/echo-plugin\"\nMaxConcurrency = 1\n[ServiceNode.CBORPluginKaetzchen.Config]\nlog_dir = \"/var/log/katzenpost\"\n"
	plan, err := PlanCandidate(repo, "namenlos", []CandidateEdit{{Path: "nodes/lilly-servicenode.toml", Base: string(body), Text: updated}})
	if err != nil {
		t.Fatal(err)
	}
	err = ApplyLifecycle(plan)
	if err == nil || !strings.Contains(err.Error(), `service capability "echo"`) {
		t.Fatalf("candidate apply error = %v, want duplicate active capability rejection", err)
	}
}

func TestNewNodeRejectsDuplicateActiveCapabilitiesAcrossKinds(t *testing.T) {
	repo := lifecycleRepo(t)
	t.Setenv("KNO_REPO", repo)
	public := testPublicKeyFile(t, "Ed25519 Sphincs+")
	identity, err := os.ReadFile(public)
	if err != nil {
		t.Fatal(err)
	}
	body := servicenodeBody("duplicate", "duplicate-pq-servicenode", "duplicate_id_pub.pem", string(identity), "tcp://198.51.100.89:4242")
	body += "\n[[ServiceNode.CBORPluginKaetzchen]]\nCapability = \"echo\"\nCommand = \"/usr/bin/echo-plugin\"\nMaxConcurrency = 1\n[ServiceNode.CBORPluginKaetzchen.Config]\nlog_dir = \"/var/log/katzenpost\"\n"
	from := filepath.Join(t.TempDir(), "newnode.toml")
	if err := os.WriteFile(from, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runEditorCommand(t, "", "create", "--network", "namenlos", "--from", from, "--host", "lilly", "--public-key", public, "--write")
	if err == nil || !strings.Contains(err.Error(), `service capability "echo"`) {
		t.Fatalf("create error = %v, want duplicate active capability rejection; output: %s", err, out)
	}
}

func TestPluginConfigKeyCannotAddTables(t *testing.T) {
	body := "[Server]\nIdentifier = \"a\"\n\n[[ServiceNode.CBORPluginKaetzchen]]\nCapability = \"echo\"\nEndpoint = \"echo\"\nCommand = \"/usr/bin/echo-plugin\"\nMaxConcurrency = 1\nDisable = false\n[ServiceNode.CBORPluginKaetzchen.Config]\nlog_dir = \"/var/log/echo\"\n"
	key := "a = \"b\"\n[[ServiceNode.CBORPluginKaetzchen]]\nCapability = \"spam\"\nCommand = \"/tmp/spam\"\nx"
	plan, err := PlanServicePlugin(body, PluginEdit{Action: PluginUpdate, ID: "echo-plugin", Config: map[string]any{"log_dir": "/var/log/echo", key: "v"}})
	if err != nil {
		t.Logf("refused: %v", err)
		return
	}
	var decoded parsedPluginBlock
	if _, err := toml.Decode(plan.Body, &decoded); err != nil {
		t.Fatalf("plan produced invalid TOML: %v\n%s", err, plan.Body)
	}
	if len(decoded.ServiceNode.CBOR) != 1 {
		t.Fatalf("a Config key added %d plugin tables:\n%s", len(decoded.ServiceNode.CBOR), plan.Body)
	}
}
