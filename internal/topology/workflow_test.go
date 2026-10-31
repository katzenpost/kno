package topology_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/katzenpost/kno/internal/check"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/nodefile"
	"github.com/katzenpost/kno/internal/render"
	"github.com/katzenpost/kno/internal/testutil"
	"github.com/katzenpost/kno/internal/topology"
)

func operatorCoverage(t *testing.T, repo string, layers [][]string) map[string]map[int]bool {
	t.Helper()
	machines, err := nodefile.Machines(knopath.EditableDir(repo, "namenlos"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]map[int]bool{}
	for i, layer := range layers {
		for _, mix := range layer {
			host, ok := config.ServiceNode(repo, "namenlos", mix+"-pq-mixserver")
			if !ok {
				t.Fatalf("%s has no host placement", mix)
			}
			op := machines[host].Operator
			if op == "" {
				t.Fatalf("%s has no operator", mix)
			}
			if out[op] == nil {
				out[op] = map[int]bool{}
			}
			out[op][i] = true
		}
	}
	return out
}

func TestNamenlosAuditProposeRenderWorkflowPreservesOperatorCoverage(t *testing.T) {
	repo := t.TempDir()
	if err := fsutil.CopyTreeGuarded(filepath.Join(testutil.RepoRoot(t), "networks", "namenlos"), filepath.Join(repo, "networks", "namenlos")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KNO_REPO", repo)
	t.Setenv("GITHUB_STEP_SUMMARY", "")
	path := topology.ManifestPath(repo, "namenlos")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	before := strings.ReplaceAll(string(b), ", \"wauland2\"", "")
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := render.Generate(knopath.NetworkDir(repo, "namenlos"), ""); err != nil {
		t.Fatal(err)
	}
	layers, err := topology.Layers(path)
	if err != nil {
		t.Fatal(err)
	}
	coverage := operatorCoverage(t, repo, layers)
	var out strings.Builder
	audit := check.Command()
	audit.SetOut(&out)
	audit.SetErr(&out)
	audit.SetArgs([]string{"topology", "--repo", repo, "--network", "namenlos"})
	if err := audit.Execute(); err == nil || !strings.Contains(out.String(), "wauland2") {
		t.Fatalf("audit did not identify the unplaced mix: %v %s", err, out.String())
	}
	unsafe := topology.Command()
	unsafe.SetIn(strings.NewReader("1\n"))
	unsafe.SetOut(&out)
	unsafe.SetErr(&out)
	unsafe.SetArgs([]string{"--network", "namenlos", "--ask", "--apply"})
	if err := unsafe.Execute(); err == nil || !strings.Contains(err.Error(), "every mix layer") {
		t.Fatalf("unsafe operator coverage was accepted: %v", err)
	}
	b, err = os.ReadFile(path)
	if err != nil || string(b) != before {
		t.Fatal("rejected proposal changed the manifest")
	}
	proposal := topology.Command()
	proposal.SetIn(strings.NewReader("2\n"))
	proposal.SetOut(&out)
	proposal.SetErr(&out)
	proposal.SetArgs([]string{"--network", "namenlos", "--ask", "--apply"})
	if err := proposal.Execute(); err != nil {
		t.Fatal(err)
	}
	generated := render.Command()
	generated.SetArgs([]string{"--network", "namenlos"})
	if err := generated.Execute(); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	audit = check.Command()
	audit.SetOut(&out)
	audit.SetErr(&out)
	audit.SetArgs([]string{"topology", "--repo", repo, "--network", "namenlos"})
	if err := audit.Execute(); err != nil {
		t.Fatalf("audit after proposal: %v %s", err, out.String())
	}
	layers, err = topology.Layers(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(coverage, operatorCoverage(t, repo, layers)) {
		t.Fatal("proposal expanded an operator's layer coverage")
	}
	paths, err := filepath.Glob(filepath.Join(knopath.ConfigsDir(repo, "namenlos"), "*-pq-authority.toml"))
	if err != nil || len(paths) != 6 {
		t.Fatalf("authority configs: %d %v", len(paths), err)
	}
	for _, path := range paths {
		var cfg struct {
			Topology struct {
				Layers []struct {
					Nodes []struct{ Identifier, IdentityPublicKeyPem string }
				}
			}
		}
		if _, err := toml.DecodeFile(path, &cfg); err != nil {
			t.Fatal(err)
		}
		found := 0
		for i, layer := range cfg.Topology.Layers {
			for _, n := range layer.Nodes {
				if n.Identifier == "wauland2" {
					found++
					if i != 2 || n.IdentityPublicKeyPem != "wauland2_mix_id_pub.key.pem" {
						t.Fatalf("unsafe or incorrect placement in %s", path)
					}
				}
			}
		}
		if found != 1 {
			t.Fatalf("%s places the proposed mix %d times", path, found)
		}
	}
}
