package nodeedit

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

func writeFixture(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadInventoryIncludesNodesOutsideManagedHosts(t *testing.T) {
	repo := t.TempDir()
	base := filepath.Join("networks", "n")
	writeFixture(t, repo, filepath.Join(base, "editable-configs", "nodes", "mix.toml"),
		"name = \"mix\"\nrole = \"mix\"\nconfigName = \"mix-pq-mixserver\"\naddresses = [\"tcp://mix.example:443\"]\n")
	writeFixture(t, repo, filepath.Join(base, "editable-configs", "nodes", "unmanaged.toml"),
		"name = \"unmanaged\"\nrole = \"gateway\"\nconfigName = \"gateway-pq-gatewaynode\"\naddresses = [\"tcp://192.0.2.8:443\"]\n")
	writeFixture(t, repo, filepath.Join(base, "editable-configs", "machines", "host.toml"),
		"operator = \"op\"\nservices = [\"mix-pq-mixserver\"]\n")
	writeFixture(t, repo, filepath.Join(base, "editable-configs", "manifest.toml"),
		"[[topology.layer]]\nmixes = [\"mix\"]\n")
	writeFixture(t, repo, filepath.Join(base, ".generated", "configs", "mix-pq-mixserver.toml"),
		"[Server]\nBindAddresses = [\"tcp://0.0.0.0:443\"]\n")

	records, err := LoadInventory(repo, "n")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want every node", len(records))
	}
	var mix, unmanaged NodeRecord
	for _, record := range records {
		if record.Name == "mix" {
			mix = record
		} else if record.Name == "unmanaged" {
			unmanaged = record
		}
	}
	if mix.Host != "host" || mix.Operator != "op" || !reflect.DeepEqual(mix.Services, []string{"mix-pq-mixserver"}) {
		t.Errorf("managed node details = %+v", mix)
	}
	if !reflect.DeepEqual(mix.Layers, []int{0}) || !reflect.DeepEqual(mix.Binds, []string{"tcp://0.0.0.0:443"}) {
		t.Errorf("placement/binds = %+v / %v", mix.Layers, mix.Binds)
	}
	if unmanaged.Host != "" {
		t.Errorf("unmatched host = %+v", unmanaged)
	}

}

func TestLoadInventoryUsesMachineDefaultsAndOverrides(t *testing.T) {
	repo := testutil.RepoRoot(t)
	records, err := LoadInventory(repo, "namenlos")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]NodeRecord{}
	for _, record := range records {
		byName[record.Name] = record
	}
	lilly := byName["lilly"]
	if lilly.Host != "lilly" || lilly.Operator != "annares" {
		t.Errorf("lilly machine defaults = host %q, operator %q", lilly.Host, lilly.Operator)
	}
	replica := byName["storagereplica2"]
	if replica.Host != "windfall2" || replica.Operator != "windfall" {
		t.Errorf("explicit service placement = host %q, operator %q", replica.Host, replica.Operator)
	}
	bound := byName["storagereplica3"]
	if len(bound.Binds) != 1 || bound.Binds[0] != "tcp://0.0.0.0:8181" {
		t.Errorf("replica bind addresses = %v", bound.Binds)
	}
}

func TestFilterInventorySupportsIndependentFilters(t *testing.T) {
	records := []NodeRecord{
		{Name: "a", File: "a.toml", Role: "mix", Host: "one", Operator: "ann", Services: []string{"a-pq-mixserver"}},
		{Name: "b", File: "b.toml", Role: "gateway", Host: "two", Operator: "bob"},
	}
	got := FilterInventory(records, InventoryFilter{Host: "ONE", Operator: "ann", Role: "mix", Search: "mixserver", Subset: []string{"a"}})
	if len(got) != 1 || got[0].Name != "a" {
		t.Fatalf("filtered = %+v", got)
	}
	if got := FilterInventory(records, InventoryFilter{Subset: []string{"b.toml"}}); len(got) != 1 || got[0].Name != "b" {
		t.Fatalf("file subset = %+v", got)
	}
}
