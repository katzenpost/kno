package contribute

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/katzenpost/kno/internal/config"
)

func TestOwnerOfRecognisesEveryPerNodeShape(t *testing.T) {
	for _, c := range []struct{ path, want string }{
		{"networks/namenlos/editable-configs/nodes/mixy-mix.toml", "mixy"},
		{"networks/namenlos/editable-configs/nodes/annares-storagenode-0.toml", "annares"},
		{"networks/namenlos/.generated/configs/mixy-pq-mixserver.toml", "mixy"},
		{"networks/namenlos/.generated/deployments/mixy.toml", "mixy"},
		{"networks/namenlos/editable-configs/keys/mix-id-keys/mixy_mix_id_pub_key.pem", "mixy"},
		{"networks/namenlos/editable-configs/keys/mixserver-keys/annares.linkpubkey.pem", "annares"},
	} {
		got, ok := OwnerOf(c.path, []string{"mixy", "annares", "reed"}, nil)
		if !ok || got != c.want {
			t.Errorf("OwnerOf(%q) = %q, %v; want %q", c.path, got, ok, c.want)
		}
	}
}

func TestOwnerOfPrefersTheLongestMatchingNode(t *testing.T) {
	nodes := []string{"wauland", "wauland2"}
	got, ok := OwnerOf("networks/namenlos/editable-configs/nodes/wauland2-mix.toml", nodes, nil)
	if !ok || got != "wauland2" {
		t.Fatalf("got %q, %v; want wauland2", got, ok)
	}
}

func TestOwnerOfRequiresASeparator(t *testing.T) {
	if got, ok := OwnerOf("networks/namenlos/editable-configs/nodes/mixyface-mix.toml", []string{"mixy"}, nil); ok {
		t.Fatalf("matched %q to mixy", got)
	}
}

func TestOwnerOfCallsTheSharedFilesShared(t *testing.T) {
	for _, p := range []string{
		"networks/namenlos/editable-configs/arch.toml",
		"networks/namenlos/editable-configs/manifest.toml",
		"networks/namenlos/.generated/network.toml",
		"networks/namenlos/.generated/configs/client.toml",
		"networks/namenlos/.generated/configs/thinclient.toml",
		"networks/namenlos/.generated/configs/pki.toml",
		"networks/namenlos/.generated/configs/courier.toml",
		"networks/namenlos/.generated/configs/catshadow.toml",
	} {
		if Shared(p) != true {
			t.Errorf("%s is not recognised as a shared file", p)
		}
		if _, ok := OwnerOf(p, []string{"mixy"}, nil); ok {
			t.Errorf("%s was attributed to a single node", p)
		}
	}
}

func TestReleaseRecordsAreNeverContributed(t *testing.T) {
	p := "networks/namenlos/releases/v0.0.105/SHA256SUMS"
	if !Excluded(p) {
		t.Fatal("a release record was not excluded")
	}
	if Shared(p) {
		t.Error("a release record was called a shared file")
	}
}

func TestOwnersReadsTheNodeFilesConfigName(t *testing.T) {
	dir := t.TempDir()
	nodes := filepath.Join(dir, "networks", "n", "editable-configs", "nodes")
	if err := os.MkdirAll(nodes, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(nodes, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("storagereplica0-storagenode.toml", "name = \"storagereplica0\"\n"+
		"configName = \"annares-pq-storagenode-0\"\n"+
		"identityKeyFile = \"storage_replica_0.public.pem\"\n")
	write("windfallgw-gateway.toml", "name = \"windfallgw\"\n"+
		"configName = \"windfall-pq-gatewaynode\"\n"+
		"identityKeyFile = \"windfallgw_id_pub_key.pem\"\n")

	o := Owners(dir, "n")
	for _, c := range []struct{ base, want string }{
		{"storagereplica0-storagenode.toml", "annares"},
		{"storage_replica_0.public.pem", "annares"},
		{"storagereplica0.linkpubkey.pem", "annares"},
		{"windfallgw-gateway.toml", "windfall"},
		{"windfallgw_id_pub_key.pem", "windfall"},
	} {
		if got := o[c.base]; got != c.want {
			t.Errorf("Owners[%q] = %q, want %q", c.base, got, c.want)
		}
	}
}

func TestOwnerOfUsesTheIndexBeforeThePrefix(t *testing.T) {
	idx := map[string]string{"windfallgw-gateway.toml": "windfall"}
	got, ok := OwnerOf("networks/n/editable-configs/nodes/windfallgw-gateway.toml",
		[]string{"windfall"}, idx)
	if !ok || got != "windfall" {
		t.Fatalf("OwnerOf = %q, %v; want windfall", got, ok)
	}
	if _, ok := OwnerOf("networks/n/editable-configs/nodes/windfallgw-gateway.toml",
		[]string{"luscus"}, idx); ok {
		t.Error("attributed to an operator that does not claim windfall")
	}
}

func TestAMachineFileBelongsToItsNode(t *testing.T) {
	p := "networks/namenlos/editable-configs/machines/mixy.toml"
	if Shared(p) {
		t.Errorf("%s is called shared", p)
	}
	if n, ok := OwnerOf(p, []string{"mixy", "reed"}, nil); !ok || n != "mixy" {
		t.Errorf("OwnerOf(%s) = %q %v, want mixy", p, n, ok)
	}
}

func TestOperatorOfNamesOneHandleEveryTime(t *testing.T) {
	repo := t.TempDir()
	p := filepath.Join(repo, "networks", "n", ".generated", "network.toml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "[operators.zed]\nnodes = [\"mixy\"]\n\n[operators.amy]\nnodes = [\"mixy\"]\n\n" +
		"[operators.kim]\nnodes = [\"mixy\"]\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c := config.Config{Nodes: []string{"mixy"}}
	for i := 0; i < 50; i++ {
		if got := operatorOf(repo, "n", c); got != "amy" {
			t.Fatalf("run %d: operatorOf = %q, want amy, the first handle in order", i, got)
		}
	}
}
