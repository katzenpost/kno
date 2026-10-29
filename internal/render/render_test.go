package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
