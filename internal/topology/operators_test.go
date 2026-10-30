package topology

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProposePrefersExistingOperatorLayersOverThinnerLayers(t *testing.T) {
	repo := fakeNet(t, "[[topology.layer]]\nmixes = [\"a\", \"b\"]\n[[topology.layer]]\nmixes = [\"c\"]\n[[topology.layer]]\nmixes = [\"d\"]\n", map[string]string{"new-mix": mixNode("new")})
	dir := filepath.Join(repo, "networks", "testnet", "editable-configs", "machines")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "new"} {
		if err := os.WriteFile(filepath.Join(dir, name+".toml"), []byte("operator = \"same\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := Propose(Options{Repo: repo, Network: "testnet", Log: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Placements) != 1 || p.Placements[0].Layer != 0 {
		t.Fatalf("operator gained another layer: %+v", p.Placements)
	}
}

func TestAuditRejectsAnOperatorInEveryMixLayer(t *testing.T) {
	repo := fakeNet(t, "[[topology.layer]]\nmixes = [\"a\"]\n[[topology.layer]]\nmixes = [\"b\"]\n", map[string]string{"a-mix": mixNode("a"), "b-mix": mixNode("b")})
	dir := filepath.Join(repo, "networks", "testnet", "editable-configs", "machines")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(dir, name+".toml"), []byte("operator = \"same\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	findings, err := Lint(repo, "testnet")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Subject == "operator same" && strings.Contains(f.Detail, "every mix layer") {
			return
		}
	}
	t.Fatalf("missing operator visibility finding: %v", findings)
}

func TestExplicitMixPlacementDeterminesItsOperator(t *testing.T) {
	repo := fakeNet(t, sampleManifest, map[string]string{"new-mix": mixNode("new")})
	dir := filepath.Join(repo, "networks", "testnet", "editable-configs", "machines")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"new":           "operator = \"other\"\n",
		"physical-host": "operator = \"actual\"\nservices = [\"new-pq-mixserver\"]\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name+".toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	owners, err := mixOperators(repo, "testnet")
	if err != nil {
		t.Fatal(err)
	}
	if owners["new"] != "actual" {
		t.Fatalf("mix operator = %q, want actual", owners["new"])
	}
}
