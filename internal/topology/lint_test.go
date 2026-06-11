package topology

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const idKey = "-----BEGIN ED25519 SPHINCS+ PUBLIC KEY-----\nQUJD\n-----END ED25519 SPHINCS+ PUBLIC KEY-----\n"
const linkKey = "-----BEGIN X25519 PUBLIC KEY-----\nQUJD\n-----END X25519 PUBLIC KEY-----\n"

func lintNet(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	net := filepath.Join(repo, "networks", "testnet", "editable-configs")
	for _, d := range []string{"nodes", "keys/mix-id-keys", "keys/gateway-id-keys"} {
		if err := os.MkdirAll(filepath.Join(net, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(net, "manifest.toml"),
		lintManifest("[[topology.layer]]\nmixes = [\"aaa\"]\n\n[[topology.layer]]\nmixes = [\"bbb\"]\n"))
	for _, n := range []string{"aaa", "bbb"} {
		write(t, filepath.Join(net, "nodes", n+"-mix.toml"),
			"name = \""+n+"\"\nrole = \"mix\"\nconfigName = \""+n+"-pq-mixserver\"\n"+
				"addresses = [\"tcp://198.51.100.1:4242\"]\n"+
				"identityKeyFile = \""+n+"_mix_id_pub_key.pem\"\n")
		write(t, filepath.Join(net, "keys", "mix-id-keys", n+"_mix_id_pub_key.pem"), idKey)
	}
	return repo
}

var lintNodes = [][2]string{{"aaa", "mix"}, {"bbb", "mix"}}

func lintManifest(layers string, extra ...[2]string) string {
	var b strings.Builder
	for _, n := range append(append([][2]string{}, lintNodes...), extra...) {
		b.WriteString("[[node]]\nname = \"" + n[0] + "\"\nrole = \"" + n[1] + "\"\n" +
			"config = \"nodes/" + n[0] + "-" + n[1] + ".toml\"\n\n")
	}
	return b.String() + layers
}

func write(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func lintStrings(t *testing.T, repo string) []string {
	t.Helper()
	f, err := Lint(repo, "testnet")
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(f))
	for _, x := range f {
		out = append(out, x.String())
	}
	return out
}

func wants(t *testing.T, got []string, substr string) {
	t.Helper()
	for _, g := range got {
		if strings.Contains(g, substr) {
			return
		}
	}
	t.Errorf("no finding mentions %q; got %v", substr, got)
}

func TestLintCleanNetworkHasNoFindings(t *testing.T) {
	if got := lintStrings(t, lintNet(t)); len(got) != 0 {
		t.Errorf("a clean network produced findings: %v", got)
	}
}

func TestLintCatchesAnUnplacedMix(t *testing.T) {
	repo := lintNet(t)
	net := filepath.Join(repo, "networks", "testnet", "editable-configs")
	write(t, filepath.Join(net, "nodes", "ccc-mix.toml"),
		"name = \"ccc\"\nrole = \"mix\"\nconfigName = \"ccc-pq-mixserver\"\n"+
			"identityKeyFile = \"ccc_mix_id_pub_key.pem\"\n")
	write(t, filepath.Join(net, "keys", "mix-id-keys", "ccc_mix_id_pub_key.pem"), idKey)
	wants(t, lintStrings(t, repo), "ccc: mix is in no topology layer")
}

func TestLintCatchesAMixInTwoLayers(t *testing.T) {
	repo := lintNet(t)
	write(t, filepath.Join(repo, "networks", "testnet", "editable-configs", "manifest.toml"),
		lintManifest("[[topology.layer]]\nmixes = [\"aaa\"]\n\n[[topology.layer]]\nmixes = [\"bbb\", \"aaa\"]\n"))
	wants(t, lintStrings(t, repo), "aaa: mix appears in 2 topology layers")
}

func TestLintCatchesALayerNamingAnUnknownNode(t *testing.T) {
	repo := lintNet(t)
	write(t, filepath.Join(repo, "networks", "testnet", "editable-configs", "manifest.toml"),
		lintManifest("[[topology.layer]]\nmixes = [\"aaa\", \"ghost\"]\n\n[[topology.layer]]\nmixes = [\"bbb\"]\n"))
	wants(t, lintStrings(t, repo), "ghost: topology layer 0 names a node that is not a mix")
}

func TestLintCatchesAMissingIdentityKey(t *testing.T) {
	repo := lintNet(t)
	if err := os.Remove(filepath.Join(repo, "networks", "testnet", "editable-configs", "keys", "mix-id-keys", "aaa_mix_id_pub_key.pem")); err != nil {
		t.Fatal(err)
	}
	wants(t, lintStrings(t, repo), "identity key aaa_mix_id_pub_key.pem not found")
}

func TestLintCatchesALinkKeyInAnIdentitySlot(t *testing.T) {
	repo := lintNet(t)
	write(t, filepath.Join(repo, "networks", "testnet", "editable-configs", "keys", "mix-id-keys", "aaa_mix_id_pub_key.pem"), linkKey)
	wants(t, lintStrings(t, repo), "is not an Ed25519 Sphincs+ identity key")
}

func TestLintCatchesAnOrphanKey(t *testing.T) {
	repo := lintNet(t)
	write(t, filepath.Join(repo, "networks", "testnet", "editable-configs", "keys", "mix-id-keys", "ghost_mix_id_pub_key.pem"), idKey)
	wants(t, lintStrings(t, repo), "is referenced by no node")
}

func TestLintAllowsANameSharedAcrossRoles(t *testing.T) {
	repo := lintNet(t)
	net := filepath.Join(repo, "networks", "testnet", "editable-configs")
	write(t, filepath.Join(net, "nodes", "aaa-authority.toml"),
		"name = \"aaa\"\nrole = \"authority\"\nconfigName = \"aaa-pq-authority\"\n"+
			"addresses = [\"tcp://198.51.100.2:4242\"]\n")
	write(t, filepath.Join(net, "manifest.toml"), lintManifest(
		"[[topology.layer]]\nmixes = [\"aaa\"]\n\n[[topology.layer]]\nmixes = [\"bbb\"]\n",
		[2]string{"aaa", "authority"}))
	if got := lintStrings(t, repo); len(got) != 0 {
		t.Errorf("a name shared across roles was flagged: %v", got)
	}
}

func TestLintCatchesTwoNodesOfOneRoleSharingAName(t *testing.T) {
	repo := lintNet(t)
	write(t, filepath.Join(repo, "networks", "testnet", "editable-configs", "nodes", "aaa2-mix.toml"),
		"name = \"aaa\"\nrole = \"mix\"\nconfigName = \"aaa2-pq-mixserver\"\n"+
			"identityKeyFile = \"aaa_mix_id_pub_key.pem\"\n")
	wants(t, lintStrings(t, repo), "a second mix node has this name")
}

func TestLintCatchesADuplicateConfigName(t *testing.T) {
	repo := lintNet(t)
	write(t, filepath.Join(repo, "networks", "testnet", "editable-configs", "nodes", "ccc-mix.toml"),
		"name = \"ccc\"\nrole = \"mix\"\nconfigName = \"aaa-pq-mixserver\"\n"+
			"identityKeyFile = \"aaa_mix_id_pub_key.pem\"\n")
	wants(t, lintStrings(t, repo), "configName aaa-pq-mixserver also used by")
}

func TestLintErrorsOnANetworkThatIsNotThere(t *testing.T) {
	if _, err := Lint(t.TempDir(), "nosuch"); err == nil {
		t.Fatal("want an error for a network with no nodes directory")
	}
}

func TestKnownMatchesOnTheWholeSubject(t *testing.T) {
	known := []string{"wauland2"}
	if !Known(known, Finding{Subject: "wauland2", Detail: "reworded entirely"}) {
		t.Error("the allowlist should still cover wauland2 after a re-wording")
	}
	if Known(known, Finding{Subject: "wauland", Detail: "mix is in no topology layer"}) {
		t.Error("an entry for wauland2 must not cover wauland")
	}
	if Known(known, Finding{Subject: "newmix", Detail: "mix is in no topology layer"}) {
		t.Error("the allowlist must not cover a subject it never named")
	}
	if Known(nil, Finding{Subject: "wauland2"}) {
		t.Error("an empty allowlist covers nothing")
	}
}

func TestLintCatchesARoleOutsideTheKnownOnes(t *testing.T) {
	repo := lintNet(t)
	net := filepath.Join(repo, "networks", "testnet", "editable-configs")
	write(t, filepath.Join(net, "nodes", "ccc-Authority.toml"),
		"name = \"ccc\"\nrole = \"Authority\"\nconfigName = \"ccc-pq-authority\"\n"+
			"addresses = [\"tcp://198.51.100.3:4242\"]\n")
	wants(t, lintStrings(t, repo), `ccc: role "Authority" is not one of`)
}

func TestLintCatchesANodeFileTheManifestDoesNotList(t *testing.T) {
	repo := lintNet(t)
	net := filepath.Join(repo, "networks", "testnet", "editable-configs")
	write(t, filepath.Join(net, "nodes", "ccc-authority.toml"),
		"name = \"ccc\"\nrole = \"authority\"\nconfigName = \"ccc-pq-authority\"\n"+
			"addresses = [\"tcp://198.51.100.3:4242\"]\n")
	wants(t, lintStrings(t, repo), "nodes/ccc-authority.toml: no [[node]] in manifest.toml")
}

func TestLintCatchesAManifestEntryWithNoNodeFile(t *testing.T) {
	repo := lintNet(t)
	write(t, filepath.Join(repo, "networks", "testnet", "editable-configs", "manifest.toml"),
		lintManifest("[[topology.layer]]\nmixes = [\"aaa\"]\n\n[[topology.layer]]\nmixes = [\"bbb\"]\n",
			[2]string{"ghost", "gateway"}))
	wants(t, lintStrings(t, repo), "nodes/ghost-gateway.toml: manifest.toml names")
}

func TestLintCatchesAManifestEntryThatDisagreesWithItsFile(t *testing.T) {
	repo := lintNet(t)
	net := filepath.Join(repo, "networks", "testnet", "editable-configs")
	write(t, filepath.Join(net, "manifest.toml"), strings.Replace(
		lintManifest("[[topology.layer]]\nmixes = [\"aaa\"]\n\n[[topology.layer]]\nmixes = [\"bbb\"]\n"),
		"name = \"bbb\"\nrole = \"mix\"", "name = \"bbb\"\nrole = \"gateway\"", 1))
	wants(t, lintStrings(t, repo), "manifest.toml names bbb (gateway), but the file says bbb (mix)")
}

func TestLintCatchesAMissingOrRepeatedReplicaID(t *testing.T) {
	repo := lintNet(t)
	net := filepath.Join(repo, "networks", "testnet", "editable-configs")
	if err := os.MkdirAll(filepath.Join(net, "keys", "replica-id-keys"), 0o755); err != nil {
		t.Fatal(err)
	}
	var extra [][2]string
	for name, id := range map[string]string{"r0": "replicaID = 0\n", "r1": "replicaID = 0\n", "r2": ""} {
		write(t, filepath.Join(net, "nodes", name+"-storagenode.toml"),
			"name = \""+name+"\"\nrole = \"storagenode\"\nconfigName = \""+name+"-pq-storagenode\"\n"+
				"addresses = [\"tcp://198.51.100.4:4242\"]\n"+id+
				"identityKeyFile = \""+name+".pem\"\n")
		write(t, filepath.Join(net, "keys", "replica-id-keys", name+".pem"), idKey)
		extra = append(extra, [2]string{name, "storagenode"})
	}
	write(t, filepath.Join(net, "manifest.toml"), lintManifest(
		"[[topology.layer]]\nmixes = [\"aaa\"]\n\n[[topology.layer]]\nmixes = [\"bbb\"]\n", extra...))
	got := lintStrings(t, repo)
	wants(t, got, "r1: replicaID 0 also used by nodes/r0-storagenode.toml")
	wants(t, got, "r2: storage node without replicaID")
}
