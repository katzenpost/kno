package nodeedit

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func commandFixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	base := filepath.Join("networks", "n")
	writeFixture(t, repo, filepath.Join(base, "editable-configs", "nodes", "mix.toml"),
		"name = \"mix\"\nrole = \"mix\"\nconfigName = \"mix-pq-mixserver\"\naddresses = [\"tcp://mix.example:443\"]\n")
	writeFixture(t, repo, filepath.Join(base, "editable-configs", "nodes", "unmanaged.toml"),
		"name = \"unmanaged\"\nrole = \"gateway\"\nconfigName = \"gateway-pq-gatewaynode\"\n")
	writeFixture(t, repo, filepath.Join(base, "editable-configs", "machines", "host.toml"),
		"operator = \"op\"\nservices = [\"mix-pq-mixserver\"]\n")
	t.Setenv("KNO_REPO", repo)
	return repo
}

func executeNodeCommand(t *testing.T, cmdArgs ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd := testCommand(cmdArgs...)
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := cmd.Execute()
	return out.String(), err
}

func testCommand(args ...string) *cobra.Command {
	root := &cobra.Command{Use: "kno"}
	root.AddCommand(NodesCommand(), NodeCommand())
	root.SetArgs(args)
	return root
}

func hasInventoryRow(out, name string) bool {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == name {
			return true
		}
	}
	return false
}

func hasRawTerminalControl(value string) bool {
	for _, r := range value {
		if (r < 0x20 && r != '\n' && r != '\r' && r != '\t') || r == 0x7f {
			return true
		}
	}
	return false
}

func TestNodesCommandListsAllNodesAndFilters(t *testing.T) {
	commandFixture(t)
	out, err := executeNodeCommand(t, "nodes", "--network", "n")
	if err != nil {
		t.Fatal(err)
	}
	if !hasInventoryRow(out, "mix") || !hasInventoryRow(out, "unmanaged") {
		t.Fatalf("default inventory should include managed and unmanaged nodes: %s", out)
	}

	out, err = executeNodeCommand(t, "nodes", "--network", "n", "--operator", "op")
	if err != nil {
		t.Fatal(err)
	}
	if !hasInventoryRow(out, "mix") || hasInventoryRow(out, "unmanaged") {
		t.Fatalf("operator filter result = %s", out)
	}
}

func TestNodeShowReportsMatchedConfigurationAndJSON(t *testing.T) {
	commandFixture(t)
	out, err := executeNodeCommand(t, "node", "show", "mix", "--network", "n")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Node mix", "Node file: mix.toml", "Daemon config: mix-pq-mixserver", "Physical host: host"} {
		if !strings.Contains(out, want) {
			t.Errorf("node details missing %q: %s", want, out)
		}
	}
	if !strings.Contains(out, "Layer: unplaced") {
		t.Errorf("unplaced mix should show its topology status: %s", out)
	}

	out, err = executeNodeCommand(t, "node", "show", "unmanaged", "--network", "n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Layer:") {
		t.Errorf("non-mix node should not show topology layer status: %s", out)
	}

	out, err = executeNodeCommand(t, "nodes", "--network", "n", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Nodes []NodeRecord `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Nodes) != 2 {
		t.Fatalf("JSON inventory = %+v", payload)
	}
}

func TestInventoryHumanOutputEscapesTOMLControlValues(t *testing.T) {
	repo := commandFixture(t)
	base := filepath.Join("networks", "n", "editable-configs")
	writeFixture(t, repo, filepath.Join(base, "nodes", "mix.toml"), `name = "\u001b[2J"
role = "mix"
configName = "mix-pq-mixserver"
addresses = ["tcp://mix.example:443\u001b[2J"]
`)
	writeFixture(t, repo, filepath.Join(base, "machines", "host.toml"), `operator = "op\u007f"
services = ["mix-pq-mixserver"]
`)
	name := "\x1b[2J"
	out, err := executeNodeCommand(t, "nodes", "--network", "n")
	if err != nil {
		t.Fatal(err)
	}
	if hasRawTerminalControl(out) {
		t.Fatalf("list output contains a raw terminal control: %q", out)
	}
	out, err = executeNodeCommand(t, "node", "show", name, "--network", "n")
	if err != nil {
		t.Fatal(err)
	}
	if hasRawTerminalControl(out) {
		t.Fatalf("detail output contains a raw terminal control: %q", out)
	}

	out, err = executeNodeCommand(t, "nodes", "--network", "n", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "\x1b") {
		t.Fatalf("JSON output contains a raw terminal escape: %q", out)
	}
	var payload struct {
		Nodes []NodeRecord `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatal(err)
	}
	var hostile *NodeRecord
	for i := range payload.Nodes {
		if payload.Nodes[i].Name == name {
			hostile = &payload.Nodes[i]
		}
	}
	if len(payload.Nodes) != 2 || hostile == nil || hostile.Operator != "op\x7f" ||
		len(hostile.Addresses) != 1 || hostile.Addresses[0] != "tcp://mix.example:443\x1b[2J" {
		t.Fatalf("JSON did not preserve control values: %+v", payload.Nodes)
	}
}
