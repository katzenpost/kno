package render

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

type fixtureNode struct{ name, role, configName string }

var fixtureNodes = []fixtureNode{
	{"auth", "authority", "auth-pq-authority"},
	{"mixa", "mix", "mixa-pq-mixserver"},
	{"mixb", "mix", "mixb-pq-mixserver"},
}

func nodeBody(n fixtureNode) string {
	return "name = \"" + n.name + "\"\nrole = \"" + n.role + "\"\n" +
		"configName = \"" + n.configName + "\"\n" +
		"addresses = [\"tcp://198.51.100.1:4242\"]\n" +
		"identityKeyFile = \"" + n.name + "_id_pub_key.pem\"\n"
}

func manifest(nodes []fixtureNode, topology string) string {
	var b strings.Builder
	b.WriteString("[network]\npkiSignatureScheme = \"Ed25519 Sphincs+\"\n\n")
	for _, n := range nodes {
		b.WriteString("[[node]]\nname = \"" + n.name + "\"\nrole = \"" + n.role + "\"\n" +
			"config = \"nodes/" + n.name + "-" + n.role + ".toml\"\n\n")
	}
	b.WriteString(topology)
	return b.String()
}

const twoLayers = "[[topology.layer]]\nmixes = [\"mixa\"]\n\n[[topology.layer]]\nmixes = [\"mixb\"]\n"

func fixture(t *testing.T) string {
	t.Helper()
	net := filepath.Join(t.TempDir(), "testnet")
	src := filepath.Join(net, "editable-configs")
	for _, d := range []string{"nodes", "keys/mix-id-keys", "services"} {
		if err := os.MkdirAll(filepath.Join(src, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range fixtureNodes {
		put(t, filepath.Join(src, "nodes", n.name+"-"+n.role+".toml"), nodeBody(n))
	}
	put(t, filepath.Join(src, "manifest.toml"), manifest(fixtureNodes, twoLayers))
	return net
}

func put(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func wantError(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("no error; want one that mentions %q", substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Fatalf("error %q does not mention %q", err, substr)
	}
}

func TestRenderRefusesARoleItDoesNotKnow(t *testing.T) {
	net := fixture(t)
	n := fixtureNode{"auth", "Authority", "auth-pq-authority"}
	put(t, filepath.Join(net, "editable-configs", "nodes", "auth-authority.toml"), nodeBody(n))
	wantError(t, Render(net, t.TempDir()), `role "Authority"`)
}

func TestRenderRefusesANodeFileThatDisagreesWithTheManifest(t *testing.T) {
	net := fixture(t)
	n := fixtureNode{"mixa", "gateway", "mixa-pq-gatewaynode"}
	put(t, filepath.Join(net, "editable-configs", "nodes", "mixa-mix.toml"), nodeBody(n))
	wantError(t, Render(net, t.TempDir()), "manifest.toml names mixa (mix)")
}

func TestRenderRefusesATopologyMixItDoesNotRender(t *testing.T) {
	net := fixture(t)
	put(t, filepath.Join(net, "editable-configs", "manifest.toml"),
		manifest(fixtureNodes, twoLayers+"\n[[topology.layer]]\nmixes = [\"ghost\"]\n"))
	wantError(t, Render(net, t.TempDir()), "the topology names ghost")
}

func TestRenderRefusesNodeOutputPathsThatEscapeTheDestination(t *testing.T) {
	for _, tc := range []struct {
		name       string
		role       string
		configName string
		fallback   bool
		wantFile   string
	}{
		{"mix configName", "mix", "../../escaped", false, "escaped.toml"},
		{"authority configName", "authority", "../../escaped", false, "escaped.toml"},
		{"mix name fallback", "mix", "", true, "escaped-pq-mixserver.toml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			net := fixture(t)
			var node fixtureNode
			for _, candidate := range fixtureNodes {
				if candidate.role == tc.role {
					node = candidate
					break
				}
			}
			if node.role == "" {
				t.Fatal("fixture node not found")
			}
			if tc.fallback {
				node.name = "../../escaped"
				node.configName = ""
				put(t, filepath.Join(net, "editable-configs", "nodes", "mixa-mix.toml"), nodeBody(node))
				m := strings.Replace(manifest(fixtureNodes, twoLayers), `name = "mixa"`, `name = "../../escaped"`, 1)
				m = strings.Replace(m, `mixes = ["mixa"]`, `mixes = ["../../escaped"]`, 1)
				put(t, filepath.Join(net, "editable-configs", "manifest.toml"), m)
			} else {
				node.configName = tc.configName
				put(t, filepath.Join(net, "editable-configs", "nodes", node.name+"-"+node.role+".toml"), nodeBody(node))
			}
			base := t.TempDir()
			out := filepath.Join(base, "published", "configs")
			sentinel := filepath.Join(out, "existing.toml")
			if err := os.MkdirAll(out, 0o755); err != nil {
				t.Fatal(err)
			}
			put(t, sentinel, "existing output\n")
			wantError(t, Render(net, out), "bad filename")
			if _, err := os.Lstat(filepath.Join(base, tc.wantFile)); !os.IsNotExist(err) {
				t.Errorf("escaped output exists or could not be checked: %v", err)
			}
			got, err := os.ReadFile(sentinel)
			if err != nil || string(got) != "existing output\n" {
				t.Errorf("existing output changed: %q, %v", got, err)
			}
		})
	}
}

func TestGeneratePreservesPublishedFilesWhenValidationFails(t *testing.T) {
	net := fixture(t)
	put(t, filepath.Join(net, "editable-configs", "manifest.toml"),
		manifest(fixtureNodes, twoLayers+"\n[[topology.layer]]\nmixes = [\"ghost\"]\n"))
	gen := t.TempDir()
	files := map[string]string{
		"configs/mixa-pq-mixserver.toml": "previous config\n",
		"configs/removed-node.toml":      "unrelated generated file\n",
		"services/operator.conf":         "previous service\n",
		"deployments/release-record":     "release record\n",
	}
	if err := os.MkdirAll(gen, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		path := filepath.Join(gen, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		put(t, path, body)
	}
	if err := Generate(net, gen); err == nil {
		t.Fatal("Generate succeeded with an unknown topology mix")
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(gen, filepath.FromSlash(name)))
		if err != nil {
			t.Errorf("published %s was removed: %v", name, err)
		} else if string(got) != want {
			t.Errorf("published %s changed to %q, want %q", name, got, want)
		}
	}
}

func TestGenerateRefusesInlinePrivateKeysBeforePublishing(t *testing.T) {
	net := fixture(t)
	path := filepath.Join(net, "editable-configs", "nodes", "auth-authority.toml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	put(t, path, string(body)+`identityPublicKey = """
-----BEGIN PRIVATE KEY-----
test-only-key-material
-----END PRIVATE KEY-----
"""
`)
	gen := t.TempDir()
	files := map[string]string{
		"configs/previous.toml": "previous config\n",
		"network.toml":          "previous network\n",
		"deployments/release":   "release record\n",
	}
	for name, content := range files {
		path := filepath.Join(gen, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		put(t, path, content)
	}
	wantError(t, Generate(net, gen), "private key")
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(gen, filepath.FromSlash(name)))
		if err != nil {
			t.Errorf("published %s was removed: %v", name, err)
		} else if string(got) != want {
			t.Errorf("published %s changed to %q, want %q", name, got, want)
		}
	}
}

func TestRecoverDirectoryRestoresAnInterruptedPublication(t *testing.T) {
	out := filepath.Join(t.TempDir(), "generated")
	backup := backupPath(out)
	previous := filepath.Join(backup, "configs", "previous.toml")
	if err := os.MkdirAll(filepath.Dir(previous), 0o755); err != nil {
		t.Fatal(err)
	}
	put(t, previous, "previous output\n")
	if err := recoverDirectory(out); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(out, "configs", "previous.toml"))
	if err != nil || string(got) != "previous output\n" {
		t.Fatalf("previous output was not restored: %q, %v", got, err)
	}
}

func TestRecoverDirectoryFinishesAPublishedSwap(t *testing.T) {
	out := filepath.Join(t.TempDir(), "generated")
	backup := backupPath(out)
	for path, body := range map[string]string{
		filepath.Join(out, "configs", "candidate.toml"):   "candidate\n",
		filepath.Join(backup, "configs", "previous.toml"): "previous\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		put(t, path, body)
	}
	if err := recoverDirectory(out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "configs", "candidate.toml")); err != nil {
		t.Errorf("published candidate was removed: %v", err)
	}
	if _, err := os.Lstat(backup); !os.IsNotExist(err) {
		t.Errorf("recovery left %s behind: %v", backup, err)
	}
}

func TestGenerateRemovesTheFilesOfARemovedNode(t *testing.T) {
	net := fixture(t)
	gen := t.TempDir()
	if err := Generate(net, gen); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"configs/mixb-pq-mixserver.toml", "services/mixb-pq-mixserver.conf"} {
		if _, err := os.Stat(filepath.Join(gen, p)); err != nil {
			t.Fatalf("the first render wrote no %s: %v", p, err)
		}
	}
	put(t, filepath.Join(net, "editable-configs", "manifest.toml"),
		manifest(fixtureNodes[:2], "[[topology.layer]]\nmixes = [\"mixa\"]\n"))
	if err := Generate(net, gen); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"configs/mixb-pq-mixserver.toml", "services/mixb-pq-mixserver.conf"} {
		if _, err := os.Stat(filepath.Join(gen, p)); err == nil {
			t.Errorf("%s of the removed node is still there", p)
		}
	}
	if _, err := os.Stat(filepath.Join(gen, "configs", "mixa-pq-mixserver.toml")); err != nil {
		t.Errorf("the render lost a node that stays: %v", err)
	}
}

func TestGenerateFailsWhenAServiceDoesNotResolve(t *testing.T) {
	net := fixture(t)
	bad := filepath.Join(net, "editable-configs", "services", "mixa-pq-mixserver.conf")
	if err := os.Mkdir(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	wantError(t, Generate(net, t.TempDir()), "mixa-pq-mixserver.toml")
}

func TestGenerateRefusesAmbiguousOrUnknownMachineServices(t *testing.T) {
	for _, tc := range []struct{ name, a, b, want string }{
		{"duplicate", "auth-pq-authority", "auth-pq-authority", "placed on both"},
		{"unknown", "ghost-pq-authority", "mixa-pq-mixserver", "unknown service ghost-pq-authority"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			net := fixture(t)
			dir := filepath.Join(net, "editable-configs", "machines")
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			put(t, filepath.Join(dir, "a.toml"), "operator = \"group\"\nservices = [\""+tc.a+"\"]\n")
			put(t, filepath.Join(dir, "b.toml"), "operator = \"group\"\nservices = [\""+tc.b+"\"]\n")
			wantError(t, Generate(net, t.TempDir()), tc.want)
		})
	}
}

func TestRenderWritesTheBridgeSocketOfAStorageNodeThatHasOne(t *testing.T) {
	net := fixture(t)
	nodes := append(fixtureNodes,
		fixtureNode{"sta", "storagenode", "sta-pq-storagenode-0"},
		fixtureNode{"stb", "storagenode", "stb-pq-storagenode-1"})
	wantSocket := `/var/lib/katzenpost/replica/sta-pq-storagenode-0/bridge"quoted\\socket.sock`
	put(t, filepath.Join(net, "editable-configs", "nodes", "sta-storagenode.toml"),
		nodeBody(nodes[3])+"bridgeSocket = "+strconv.Quote(wantSocket)+"\n")
	put(t, filepath.Join(net, "editable-configs", "nodes", "stb-storagenode.toml"), nodeBody(nodes[4]))
	put(t, filepath.Join(net, "editable-configs", "manifest.toml"), manifest(nodes, twoLayers))
	out := t.TempDir()
	if err := Render(net, out); err != nil {
		t.Fatal(err)
	}
	a, err := os.ReadFile(filepath.Join(out, "sta-pq-storagenode-0.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var aConfig struct{ BridgeSocket string }
	if _, err := toml.Decode(string(a), &aConfig); err != nil {
		t.Fatal(err)
	}
	if aConfig.BridgeSocket != wantSocket {
		t.Errorf("bridge socket round trip = %q, want %q", aConfig.BridgeSocket, wantSocket)
	}
	b, err := os.ReadFile(filepath.Join(out, "stb-pq-storagenode-1.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "BridgeSocket") {
		t.Errorf("a node without a bridge socket got one:\n%s", b)
	}
	put(t, filepath.Join(net, "editable-configs", "nodes", "stb-storagenode.toml"), nodeBody(nodes[4])+"bridgeSocket = \"\"\n")
	if err := Render(net, out); err != nil {
		t.Fatal(err)
	}
	bEmpty, err := os.ReadFile(filepath.Join(out, "stb-pq-storagenode-1.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, bEmpty) {
		t.Fatal("an unset bridgeSocket changed rendered bytes when explicitly empty")
	}
}
