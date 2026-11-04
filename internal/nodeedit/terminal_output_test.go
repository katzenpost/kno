package nodeedit

import (
	"bytes"
	"github.com/spf13/cobra"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestTerminalLinearEscapesRepositoryFileNames(t *testing.T) {
	repo := commandFixture(t)
	dir := filepath.Join(repo, "networks", "n", "editable-configs", "nodes")
	if err := os.Rename(filepath.Join(dir, "mix.toml"), filepath.Join(dir, "mix\x1b[2J.toml")); err != nil {
		t.Fatal(err)
	}
	out, err := runEditorCommand(t, "q\n", "terminal", "--network", "n")
	if err != nil {
		t.Fatal(err)
	}
	if hasRawTerminalControl(out) {
		t.Fatal("linear editor emitted repository-controlled terminal escape")
	}
	if !strings.Contains(out, `mix\x1b[2J.toml`) {
		t.Fatal("escaped repository filename was not shown")
	}
}

func TestEditorPreviewEscapesRepositoryFileNames(t *testing.T) {
	repo := lifecycleRepo(t)
	t.Setenv("KNO_REPO", repo)
	net := filepath.Join(repo, "networks", "namenlos", "editable-configs")
	old := "lilly-servicenode.toml"
	name := "lilly\x1b[2J-servicenode.toml"
	if err := os.Rename(filepath.Join(net, "nodes", old), filepath.Join(net, "nodes", name)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(net, "manifest.toml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(raw), strconv.Quote("nodes/"+old), strings.ReplaceAll(strconv.Quote("nodes/"+name), `\x1b`, `\u001b`), 1)
	if updated == string(raw) {
		t.Fatal("manifest reference not updated")
	}
	if err := os.WriteFile(path, []byte(updated), 0644); err != nil {
		t.Fatal(err)
	}
	out, err := runEditorCommand(t, "", "edit", name, "--network", "namenlos", "--addresses", "tcp://127.0.0.1:19444")
	if err != nil {
		t.Fatal(err)
	}
	if hasRawTerminalControl(out) {
		t.Fatal("CLI editor preview emitted repository-controlled terminal escape")
	}
}

func TestTerminalMenuEscapesUntrustedMetadata(t *testing.T) {
	var out bytes.Buffer
	showTerminal(&out, "network\x1b[2J", []NodeRecord{{File: "file\x1b[2J.toml", Role: "role\x1b[2J", Host: "host\x1b[2J"}})
	if hasRawTerminalControl(out.String()) {
		t.Fatal("terminal menu emitted metadata control bytes")
	}
	if !strings.Contains(out.String(), `\x1b[2J`) {
		t.Fatal("terminal menu did not show escaped metadata")
	}
}

func TestTerminalHostOptionsEscapeOperatorMetadata(t *testing.T) {
	repo := commandFixture(t)
	path := filepath.Join(repo, "networks", "n", "editable-configs", "machines", "host.toml")
	if err := os.WriteFile(path, []byte("operator = \"op\\u001b[2J\"\nservices = [\"mix-pq-mixserver\"]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetIn(strings.NewReader("/cancel\n"))
	cmd.SetOut(&out)
	if _, err := promptTerminalHost(cmd, repo, "n"); err == nil {
		t.Fatal("cancelled host prompt succeeded")
	}
	if hasRawTerminalControl(out.String()) {
		t.Fatal("host prompt emitted operator control bytes")
	}
}

func TestPluginListEscapesConfiguredCapability(t *testing.T) {
	repo := commandFixture(t)
	path := filepath.Join(repo, "networks", "n", "editable-configs", "nodes", "unmanaged.toml")
	body := `name = "unmanaged"
role = "servicenode"
configName = "gateway-pq-gatewaynode"
[[ServiceNode.CBORPluginKaetzchen]]
Capability = "custom\u001b[2J"
Command = "/usr/bin/custom"
`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	out, err := runEditorCommand(t, "", "plugin", "list", "unmanaged", "--network", "n")
	if err != nil {
		t.Fatal(err)
	}
	if hasRawTerminalControl(out) {
		t.Fatal("plugin list emitted configured capability control bytes")
	}
}
