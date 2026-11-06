package nodeedit

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/katzenpost/kno/internal/testutil"
)

func lifecycleRepo(t *testing.T) string {
	t.Helper()
	source := testutil.RepoRoot(t)
	repo := t.TempDir()
	copy := func(from, to string) {
		t.Helper()
		if err := copyLifecycleTree(from, to); err != nil {
			t.Fatal(err)
		}
	}
	copy(filepath.Join(source, "networks", "namenlos"), filepath.Join(repo, "networks", "namenlos"))
	copy(filepath.Join(source, "testdata"), filepath.Join(repo, "testdata"))
	for _, rel := range []string{"topology-findings.txt", filepath.Join("thirdparty", "pins.env")} {
		body, err := os.ReadFile(filepath.Join(source, rel))
		if err != nil {
			t.Fatal(err)
		}
		writeFixture(t, repo, rel, string(body))
	}
	return repo

}

func TestLifecyclePlanPublishesTopologyChangeAndPreservesKeys(t *testing.T) {
	repo := lifecycleRepo(t)
	keys := filepath.Join(repo, "networks", "namenlos", "editable-configs", "keys")
	before, err := os.ReadFile(filepath.Join(keys, "mix-id-keys", "windfall_mix_id_pub_key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanLifecycle(repo, "namenlos", []LifecycleOperation{{Action: LifecycleMoveMix, Node: "windfall", Layer: 0}})
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyLifecycle(plan); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(keys, "mix-id-keys", "windfall_mix_id_pub_key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("topology edit changed public key material")
	}
	manifest, err := os.ReadFile(filepath.Join(repo, "networks", "namenlos", "editable-configs", "manifest.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), `mixes = ["cryptonymity", "windfall"]`) {
		t.Fatalf("manifest topology was not updated: %s", manifest)
	}
}

func TestLifecyclePlanRejectsStaleSourcesWithoutPublishing(t *testing.T) {
	repo := lifecycleRepo(t)
	plan, err := PlanLifecycle(repo, "namenlos", []LifecycleOperation{{Action: LifecycleMoveMix, Node: "windfall", Layer: 0}})
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(repo, "networks", "namenlos", "editable-configs", "manifest.toml")
	if err := os.WriteFile(manifestPath, []byte("concurrent edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ApplyLifecycle(plan); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("error = %v, want stale plan rejection", err)
	}
	body, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "concurrent edit\n" {
		t.Fatalf("stale apply changed source: %q", body)
	}
}

func TestLifecyclePlanRecoversInterruptedPublication(t *testing.T) {
	repo := lifecycleRepo(t)
	net := filepath.Join(repo, "networks", "namenlos")
	backup := net + ".kno-nodeedit-backup"
	if err := os.Rename(net, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(net+".kno-nodeedit-state", []byte("prepared\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := recoverLifecycle(net); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanLifecycle(repo, "namenlos", []LifecycleOperation{{Action: LifecycleMoveMix, Node: "windfall", Layer: 0}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(net); err != nil {
		t.Fatalf("recovery did not restore the source network: %v", err)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("recovery left backup: %v", err)
	}
	if _, err := os.Stat(net + ".kno-nodeedit-state"); !os.IsNotExist(err) {
		t.Fatalf("recovery left state: %v", err)
	}
	if err := ApplyLifecycle(plan); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleRetiresServiceAndPreservesNodeIdentityFilesOutsideConfig(t *testing.T) {
	repo := lifecycleRepo(t)
	net := filepath.Join(repo, "networks", "namenlos")
	editable := filepath.Join(net, "editable-configs")
	nodePath := filepath.Join(editable, "nodes", "lilly-servicenode.toml")
	var node lifecycleNode
	if _, err := toml.DecodeFile(nodePath, &node); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(editable, "keys", "servicenode-id-keys", node.IdentityKeyFile)
	plan, err := PlanLifecycle(repo, "namenlos", []LifecycleOperation{{Action: LifecycleRetire, Node: "lilly"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyLifecycle(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(nodePath); !os.IsNotExist(err) {
		t.Fatalf("retired node file exists or could not be checked: %v", err)
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatalf("retired public identity key exists or could not be checked: %v", err)
	}
	if _, err := os.Stat(filepath.Join(net, ".generated", "configs", node.ConfigName+".toml")); !os.IsNotExist(err) {
		t.Fatalf("retired generated config exists or could not be checked: %v", err)
	}
}

func TestLifecyclePlanRejectsUnknownSelectionWithoutMutation(t *testing.T) {
	repo := lifecycleRepo(t)
	net := filepath.Join(repo, "networks", "namenlos")
	before := testutil.TreeSnapshot(t, net)
	if _, err := PlanLifecycle(repo, "namenlos", []LifecycleOperation{{Action: LifecycleMoveMix, Node: "missing", Layer: 0}}); err == nil {
		t.Fatal("invalid lifecycle plan succeeded")
	}
	after := testutil.TreeSnapshot(t, net)
	if !equalSnapshot(before, after) {
		t.Fatal("invalid plan changed the source network")
	}
}

func TestLifecycleRenderFailurePreservesPublishedNetwork(t *testing.T) {
	repo := lifecycleRepo(t)
	net := filepath.Join(repo, "networks", "namenlos")
	before := testutil.TreeSnapshot(t, net)
	path := filepath.Join(net, "editable-configs", "nodes", "wauland-mix.toml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(body), "BEGIN ED25519 SPHINCS+ PUBLIC KEY", "BEGIN PRIVATE KEY")
	text = strings.ReplaceAll(text, "END ED25519 SPHINCS+ PUBLIC KEY", "END PRIVATE KEY")
	plan, err := PlanCandidate(repo, "namenlos", []CandidateEdit{{Path: "nodes/wauland-mix.toml", Base: string(body), Text: text}})
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyLifecycle(plan); err == nil || !strings.Contains(err.Error(), "private key") {
		t.Fatalf("apply error = %v, want private key rejection", err)
	}
	after := testutil.TreeSnapshot(t, net)
	if !equalSnapshot(before, after) {
		t.Fatal("failed candidate changed published network")
	}
}

func TestLifecycleCreateRequiresANewIdentityForAnExistingName(t *testing.T) {
	repo := lifecycleRepo(t)
	body, err := os.ReadFile(filepath.Join(repo, "networks", "namenlos", "editable-configs", "nodes", "waulandgateway-gateway.toml"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(body), `name = "waulandgateway"`, `name = "wauland"`)
	text = strings.ReplaceAll(text, `configName = "wauland-pq-gatewaynode"`, `configName = "wauland-new-pq-gatewaynode"`)
	_, err = PlanLifecycle(repo, "namenlos", []LifecycleOperation{{Action: LifecycleCreate, File: "wauland-gateway-new.toml", Text: text, Layer: -1}})
	if err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("error = %v, want identity reuse rejection", err)
	}
}

func TestLifecycleRejectsReusedLinkIdentityWithNewSigningIdentity(t *testing.T) {
	repo := lifecycleRepo(t)
	netDir := filepath.Join(repo, "networks", "namenlos")
	editable := filepath.Join(netDir, "editable-configs")
	var manifest lifecycleManifest
	if _, err := toml.DecodeFile(filepath.Join(editable, "manifest.toml"), &manifest); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(editable, "nodes", "waulandgateway-gateway.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var node lifecycleNode
	if _, err := toml.Decode(string(body), &node); err != nil {
		t.Fatal(err)
	}
	node.Name = "new-gateway"
	node.ConfigName = "new-gateway-pq-gatewaynode"
	node.IdentityPublicKey = "new unique signing identity"
	node.IdentityKeyFile = "new-gateway-identity.pub"
	keyPath := filepath.Join(editable, "keys", "gateway-id-keys", node.IdentityKeyFile)
	if err := os.WriteFile(keyPath, []byte(node.IdentityPublicKey), 0o644); err != nil {
		t.Fatal(err)
	}
	err = validateNewLifecycleIdentity(netDir, manifest, node, "", "")
	if err == nil || !strings.Contains(err.Error(), "link identity is already used") {
		t.Fatalf("error = %v, want reused link identity rejection", err)
	}
}

func TestLifecycleBlocksLayerCountChangeWithoutGeometry(t *testing.T) {
	repo := lifecycleRepo(t)
	before, err := lifecycleTreeHash(filepath.Join(repo, "networks", "namenlos"))
	if err != nil {
		t.Fatal(err)
	}
	for _, operations := range [][]LifecycleOperation{
		{{Action: LifecycleAddLayer}, {Action: LifecycleMoveMix, Node: "wauland2", Layer: 3}},
		{{Action: LifecycleDelLayer, Layer: 2, Relocate: true, TargetLayer: 1}},
	} {
		if _, err := PlanLifecycle(repo, "namenlos", operations); err == nil || !strings.Contains(err.Error(), "geometry") {
			t.Fatalf("error = %v, want geometry requirement", err)
		}
		after, err := lifecycleTreeHash(filepath.Join(repo, "networks", "namenlos"))
		if err != nil {
			t.Fatal(err)
		}
		if before != after {
			t.Fatal("blocked topology layer count change modified network sources")
		}
	}
}

func equalSnapshot(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for name, body := range a {
		if b[name] != body {
			return false
		}
	}
	return true
}

func TestLifecyclePublishesFromGroupWritableCheckout(t *testing.T) {
	old := syscall.Umask(0o002)
	defer syscall.Umask(old)
	repo := lifecycleRepo(t)
	netDir := filepath.Join(repo, "networks", "namenlos")
	err := filepath.WalkDir(netDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}
		return os.Chmod(path, 0o664)
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanLifecycle(repo, "namenlos", []LifecycleOperation{{Action: LifecycleMoveMix, Node: "windfall", Layer: 0}})
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyLifecycle(plan); err != nil {
		t.Fatal(err)
	}
}
