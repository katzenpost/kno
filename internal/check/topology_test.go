package check

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
	"github.com/katzenpost/kno/internal/topology"
)

const lintIDKey = "-----BEGIN ED25519 SPHINCS+ PUBLIC KEY-----\nQUJD\n-----END ED25519 SPHINCS+ PUBLIC KEY-----\n"

func lintNetwork(t *testing.T, repo, network string, placed, unplaced []string) {
	t.Helper()
	net := filepath.Join(repo, "networks", network, "editable-configs")
	for _, d := range []string{"nodes", filepath.Join("keys", "mix-id-keys")} {
		if err := os.MkdirAll(filepath.Join(net, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var manifest strings.Builder
	for _, m := range append(append([]string{}, placed...), unplaced...) {
		fmt.Fprintf(&manifest, "[[node]]\nname = %q\nrole = \"mix\"\nconfig = \"nodes/%s-mix.toml\"\n\n", m, m)
	}
	for _, m := range placed {
		fmt.Fprintf(&manifest, "[[topology.layer]]\nmixes = [%q]\n\n", m)
	}
	writeLintFile(t, filepath.Join(net, "manifest.toml"), manifest.String())
	for _, m := range append(append([]string{}, placed...), unplaced...) {
		writeLintFile(t, filepath.Join(net, "nodes", m+"-mix.toml"),
			fmt.Sprintf("name = %q\nrole = \"mix\"\nconfigName = %q\nidentityKeyFile = %q\n",
				m, m+"-pq-mixserver", m+"_mix_id_pub_key.pem"))
		writeLintFile(t, filepath.Join(net, "keys", "mix-id-keys", m+"_mix_id_pub_key.pem"), lintIDKey)
	}
}

func writeLintFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTopologyLintsEveryNetworkInCheckAll(t *testing.T) {
	repo := testutil.RepoRoot(t)
	all, err := All(repo, "namenlos")
	if err != nil {
		t.Fatal(err)
	}
	var topo []Finding
	for _, f := range all.Findings {
		if f.Check == "topology" {
			topo = append(topo, f)
		}
	}
	every, err := Topology(TopologyOptions{Repo: repo})
	if err != nil {
		t.Fatal(err)
	}
	if len(topo) != len(every.Findings) {
		t.Fatalf("check all reported %d topology finding(s), linting every network reports %d: %v",
			len(topo), len(every.Findings), topo)
	}
	for _, f := range topo {
		if f.Status == Skip {
			t.Errorf("every network is linted now, so nothing may skip: %s", f)
		}
	}
}

func TestTopologyLintsTheProductionNetwork(t *testing.T) {
	repo := testutil.RepoRoot(t)
	r, err := Topology(TopologyOptions{Repo: repo, Network: "namenlos"})
	if err != nil {
		t.Fatal(err)
	}
	if bad := failures(r); len(bad) > 0 {
		t.Fatalf("new topology inconsistency:\n%s", strings.Join(bad, "\n"))
	}
	if len(r.Findings) == 0 {
		t.Fatal("nothing was linted")
	}
	for _, f := range r.Findings {
		if f.Status == Skip {
			t.Errorf("the production network must not skip: %s", f)
		}
	}
}

func TestTopologyReportsOnlyKnownGaps(t *testing.T) {
	repo := testutil.RepoRoot(t)
	known, err := topology.KnownFindings(repo)
	if err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(filepath.Join(repo, "networks"))
	if err != nil {
		t.Fatal(err)
	}
	var nets []string
	for _, e := range ents {
		if e.IsDir() {
			nets = append(nets, e.Name())
		}
	}
	for _, net := range nets {
		r, err := Topology(TopologyOptions{Repo: repo, Network: net})
		if err != nil {
			t.Fatal(err)
		}
		if bad := failures(r); len(bad) > 0 {
			t.Errorf("%s: new topology inconsistency:\n%s", net, strings.Join(bad, "\n"))
		}
		if r.Count(Warn) > len(known) {
			t.Errorf("%s reported %d known gap(s), more than the %d in data/topology-findings.txt",
				net, r.Count(Warn), len(known))
		}
	}
}

func TestTopologyFailsOnNewFinding(t *testing.T) {
	repo := t.TempDir()
	lintNetwork(t, repo, "n", []string{"placed"}, []string{"unplaced"})
	r, err := Topology(TopologyOptions{Repo: repo, Network: "n", Known: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Failed() {
		t.Fatalf("with no known findings every gap is new and must fail: %v", r.Findings)
	}
}

func TestTopologyFailsWhenANetworkCannotBeLinted(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "networks", "nothing"), 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := Topology(TopologyOptions{Repo: repo, Network: "nothing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Findings) != 1 || !r.Failed() {
		t.Fatalf("want one failure, got %v", r.Findings)
	}
}

func TestTopologyDoesNotBlameOneNetworkForAnothersAllowlist(t *testing.T) {
	repo := testutil.RepoRoot(t)
	real, err := topology.KnownFindings(repo)
	if err != nil {
		t.Fatal(err)
	}
	known := append(append([]string{}, real...), "a gap only some other network reports")

	one, err := Topology(TopologyOptions{Repo: repo, Network: "namenlos", Known: known})
	if err != nil {
		t.Fatal(err)
	}
	resolved := func(r Result) int {
		n := 0
		for _, f := range r.Findings {
			if strings.HasPrefix(f.Subject, "resolved ") {
				n++
			}
		}
		return n
	}
	if got := resolved(one); got != 0 {
		t.Errorf("linting one network claimed %d resolved entry(s) it cannot know about: %v", got, one.Findings)
	}

	all, err := Topology(TopologyOptions{Repo: repo, Known: known})
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved(all); got != 1 {
		t.Errorf("want the one unreported entry called resolved, got %d: %v", got, all.Findings)
	}
}

func TestTopologyGivesEveryGapItsOwnSubject(t *testing.T) {
	repo := t.TempDir()
	lintNetwork(t, repo, "n", []string{"aaa"}, []string{"bbb", "ccc"})
	r, err := Topology(TopologyOptions{Repo: repo, Network: "n", Known: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Count(Fail) < 2 {
		t.Fatalf("needs at least two gaps to collide, got %v", r.Findings)
	}

	bySubject(t, r)
}
