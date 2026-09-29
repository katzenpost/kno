package nodeedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func seed(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	nodes := filepath.Join(repo, "networks", "n", "editable-configs", "nodes")
	if err := os.MkdirAll(nodes, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"storagereplica0-storagenode.toml": "name = \"storagereplica0\"\n" +
			"configName = \"annares-pq-storagenode-0\"\n" +
			"addresses = [\"tcp://1.2.3.4:1025\"]\n",
		"windfallgw-gateway.toml": "name = \"windfallgw\"\n" +
			"configName = \"windfall-pq-gatewaynode\"\n" +
			"addresses = [\"tcp://5.6.7.8:443\"]\n",
	} {
		if err := os.WriteFile(filepath.Join(nodes, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func TestMineIsScopedByTheClaim(t *testing.T) {
	repo := seed(t)
	got := Mine(repo, "n", []string{"annares"})
	if len(got) != 1 || got[0].File != "storagereplica0-storagenode.toml" {
		t.Fatalf("Mine = %+v, want only annares's replica", got)
	}
	if got[0].Node != "annares" {
		t.Errorf("Node = %q, want annares", got[0].Node)
	}
}

func TestPickRefusesANodeNotClaimed(t *testing.T) {
	repo := seed(t)
	_, err := Pick(Mine(repo, "n", []string{"annares"}), "windfallgw-gateway")
	if err == nil {
		t.Fatal("picked a node the config does not claim")
	}
	if !strings.Contains(err.Error(), "storagereplica0-storagenode") {
		t.Errorf("the message does not list the operator's own: %v", err)
	}
}

func TestPickTakesTheOnlyNode(t *testing.T) {
	repo := seed(t)
	n, err := Pick(Mine(repo, "n", []string{"annares"}), "")
	if err != nil {
		t.Fatal(err)
	}
	if n.File != "storagereplica0-storagenode.toml" {
		t.Errorf("Pick = %q", n.File)
	}
}

func TestParseAddressesTakesCommasOrSpaces(t *testing.T) {
	got, err := ParseAddresses(" tcp://1.2.3.4:1 , tcp://[::1]:2 ")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "tcp://1.2.3.4:1" || got[1] != "tcp://[::1]:2" {
		t.Fatalf("ParseAddresses = %v", got)
	}
	if _, err := ParseAddresses("tcp://1.2.3.4"); err == nil {
		t.Error("a bad address was accepted")
	}
}

func TestPickRefusesANodeWithMoreThanOneFile(t *testing.T) {
	repo := seed(t)
	nodes := filepath.Join(repo, "networks", "n", "editable-configs", "nodes")
	if err := os.WriteFile(filepath.Join(nodes, "windfall-mix.toml"), []byte("name = \"windfall\"\n"+
		"configName = \"windfall-pq-mixserver\"\naddresses = [\"tcp://5.6.7.9:443\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mine := Mine(repo, "n", []string{"windfall"})
	if len(mine) != 2 {
		t.Fatalf("Mine = %+v, want windfall's two files", mine)
	}
	_, err := Pick(mine, "windfall")
	if err == nil {
		t.Fatal("picked one of two files for the node name windfall")
	}
	for _, f := range []string{"windfall-mix", "windfallgw-gateway"} {
		if !strings.Contains(err.Error(), f) {
			t.Errorf("the message does not list %s: %v", f, err)
		}
	}
	if n, err := Pick(mine, "windfall-mix"); err != nil || n.File != "windfall-mix.toml" {
		t.Errorf("Pick by file = %+v, %v", n, err)
	}
}
