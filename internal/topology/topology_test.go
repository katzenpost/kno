package topology

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

const sampleManifest = `# hand-maintained; comments must survive a proposal
[network]
  [network.layout]
  Layers = 3

[[topology.layer]]
mixes = ["windfall", "cryptonymity"]

[[topology.layer]]
mixes = ["omelas", "mixy"]

[[topology.layer]]
mixes = ["oneeyedman"]
`

func fakeNet(t *testing.T, manifest string, nodes map[string]string) string {
	t.Helper()
	repo := t.TempDir()
	dir := filepath.Join(repo, "networks", "testnet", "editable-configs")
	if err := os.MkdirAll(filepath.Join(dir, "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, body := range nodes {
		if err := os.WriteFile(filepath.Join(dir, "nodes", name+".toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func mixNode(name string) string {
	return "name = \"" + name + "\"\nrole = \"mix\"\nconfigName = \"" + name + "-pq-mixserver\"\n"
}

func TestOrphansFindsUnplacedMixes(t *testing.T) {
	repo := fakeNet(t, sampleManifest, map[string]string{
		"mixy-mix":   mixNode("mixy"),
		"newbie-mix": mixNode("newbie"),
		"other-mix":  mixNode("other"),
		"gw-gateway": "name = \"gw\"\nrole = \"gateway\"\n",
	})
	got, err := Orphans(repo, "testnet")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "newbie other" {
		t.Errorf("orphans = %v, want [newbie other]", got)
	}
}

func TestOrphansIgnoresNonMixRoles(t *testing.T) {
	repo := fakeNet(t, sampleManifest, map[string]string{
		"gw-gateway": "name = \"gw\"\nrole = \"gateway\"\n",
		"auth":       "name = \"auth\"\nrole = \"authority\"\n",
	})
	got, err := Orphans(repo, "testnet")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("orphans = %v, want none", got)
	}
}

func TestProposeFillsTheThinnestLayerAndRebalances(t *testing.T) {
	repo := fakeNet(t, sampleManifest, map[string]string{
		"a-mix": mixNode("aaa"),
		"b-mix": mixNode("bbb"),
	})
	p, err := Propose(Options{Repo: repo, Network: "testnet", Log: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Placements) != 2 {
		t.Fatalf("placements = %+v, want 2", p.Placements)
	}

	if p.Placements[0].Mix != "aaa" || p.Placements[0].Layer != 2 {
		t.Errorf("first placement = %+v, want aaa in layer 2", p.Placements[0])
	}
	if p.Placements[1].Mix != "bbb" || p.Placements[1].Layer != 0 {
		t.Errorf("second placement = %+v, want bbb in layer 0 after rebalancing", p.Placements[1])
	}
}

func TestProposePreservesCommentsAndProducesValidToml(t *testing.T) {
	repo := fakeNet(t, sampleManifest, map[string]string{"n-mix": mixNode("newbie")})
	p, err := Propose(Options{Repo: repo, Network: "testnet", Log: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Manifest, "# hand-maintained") {
		t.Errorf("the manifest's comments were lost:\n%s", p.Manifest)
	}
	if !strings.Contains(p.Manifest, `mixes = ["oneeyedman", "newbie"]`) {
		t.Errorf("newbie not appended to the thinnest layer:\n%s", p.Manifest)
	}

	out := filepath.Join(t.TempDir(), "manifest.toml")
	if err := Write(p, out); err != nil {
		t.Fatal(err)
	}
	layers, err := Layers(out)
	if err != nil {
		t.Fatalf("proposed manifest does not parse: %v", err)
	}
	if len(layers) != 3 || strings.Join(layers[2], " ") != "oneeyedman newbie" {
		t.Errorf("layers = %v", layers)
	}
	if !strings.Contains(p.Diff, "+mixes =") {
		t.Errorf("diff does not show the change:\n%s", p.Diff)
	}
}

func TestProposeWritesNothingByItself(t *testing.T) {
	repo := fakeNet(t, sampleManifest, map[string]string{"n-mix": mixNode("newbie")})
	path := ManifestPath(repo, "testnet")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Propose(Options{Repo: repo, Network: "testnet", Log: io.Discard}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("Propose modified the repo; it must only ever read")
	}
}

func TestProposeIsEmptyWhenNothingIsOrphaned(t *testing.T) {
	repo := fakeNet(t, sampleManifest, map[string]string{"mixy-mix": mixNode("mixy")})
	p, err := Propose(Options{Repo: repo, Network: "testnet", Log: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Placements) != 0 || p.Manifest != "" {
		t.Errorf("proposal = %+v, want empty", p)
	}
}

func TestAskChoosesTheNamedLayer(t *testing.T) {
	repo := fakeNet(t, sampleManifest, map[string]string{"n-mix": mixNode("newbie")})
	p, err := Propose(Options{
		Repo: repo, Network: "testnet", Ask: true,
		In: strings.NewReader("0\n"), Log: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Placements[0].Layer != 0 {
		t.Errorf("layer = %d, want the answered 0", p.Placements[0].Layer)
	}
	if !strings.Contains(p.Manifest, `mixes = ["windfall", "cryptonymity", "newbie"]`) {
		t.Errorf("not placed in layer 0:\n%s", p.Manifest)
	}
}

func TestAskRejectsALayerThatDoesNotExist(t *testing.T) {
	repo := fakeNet(t, sampleManifest, map[string]string{"n-mix": mixNode("newbie")})
	_, err := Propose(Options{
		Repo: repo, Network: "testnet", Ask: true,
		In: strings.NewReader("9\n"), Log: io.Discard,
	})
	if err == nil {
		t.Fatal("want an error for a layer the network does not have")
	}
}

func TestRealNetworkOrphansMatchTheKnownFindings(t *testing.T) {
	repo := testutil.RepoRoot(t)
	got, err := Orphans(repo, "namenlos")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "topology-findings.txt")); err != nil {
		t.Fatalf("the allowlist that the lint reads is not there: %v", err)
	}
	known, err := KnownFindings(repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range got {
		if !Known(known, Finding{Subject: m}) {
			t.Errorf("mix %q is in no topology layer and is not an allowed finding;"+
				" it would run and carry no traffic. Run 'kno topology-propose'.", m)
		}
	}
	mixes, err := Mixes(repo, "namenlos")
	if err != nil {
		t.Fatal(err)
	}
	if len(mixes) < 5 {
		t.Errorf("found only %d mixes (%v); the node parser is probably wrong", len(mixes), mixes)
	}
}
