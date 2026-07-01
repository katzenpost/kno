package check

import (
	"github.com/katzenpost/kno/internal/knopath"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/testutil"
)

func TestOperatorNamenlosResolvesForEveryGroup(t *testing.T) {
	repo := testutil.RepoRoot(t)
	r, err := Operator(OperatorOptions{Repo: repo, Network: "namenlos"})
	if err != nil {
		t.Fatal(err)
	}
	if bad := failures(r); len(bad) > 0 {
		t.Fatalf("operator check failed:\n%s", strings.Join(bad, "\n"))
	}
	if n := r.Count(Skip); n != 0 {
		t.Errorf("%d skipped finding(s); namenlos pins its spot checks", n)
	}
	found := bySubject(t, r)

	ops := config.LoadOperators(repo, "namenlos")
	if len(ops) == 0 {
		t.Fatal("no operator groups")
	}
	nodes := 0
	for op, group := range ops {
		if f := found[op+" laptop config"]; f.Status != Pass {
			t.Errorf("operator %s laptop: %s", op, f)
		}
		for _, n := range group {
			nodes++
			f, ok := found[n+" services"]
			if !ok || f.Status != Pass || f.Detail == "" {
				t.Errorf("node %s services: %v", n, f)
			}
			for _, want := range []string{n + " arch", n + " os", n + " build"} {
				if f := found[want]; f.Status != Pass {
					t.Errorf("%s: %s", want, f)
				}
			}
		}
	}
	if nodes != len(config.ListNodes(repo, "namenlos")) {
		t.Errorf("checked %d nodes, but the network has %d; a node has no operator",
			nodes, len(config.ListNodes(repo, "namenlos")))
	}
	for _, s := range NamenlosSpotChecks() {
		if f := found["pinned "+s.Node+" arch"]; f.Status != Pass || f.Detail != s.Arch {
			t.Errorf("pinned %s arch: %s", s.Node, f)
		}
		for svc, want := range s.Paths {
			if f := found["pinned "+svc+" paths"]; f.Status != Pass || f.Detail != want {
				t.Errorf("pinned %s paths: %s", svc, f)
			}
		}
	}
}

func TestOperatorFlagsNodeWithoutServices(t *testing.T) {
	repo := t.TempDir()
	net := filepath.Join(repo, "networks", "fake")
	if err := os.MkdirAll(knopath.ConfigsIn(net), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(p, body string) {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(knopath.ConfigsIn(net), "alpha-pq-mixserver.toml"), "A = 1\n")
	write(knopath.NetworkFileIn(knopath.GeneratedIn(net)),
		"default = \"amd64\"\ndefault_os = \"debian-12\"\n"+
			"[operators.someone]\nnodes = [\"alpha\", \"ghost\"]\n")

	r, err := Operator(OperatorOptions{Repo: repo, Network: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	found := bySubject(t, r)
	if f := found["alpha services"]; f.Status != Pass || f.Detail != "alpha-pq-mixserver" {
		t.Errorf("alpha: %s", f)
	}
	if f := found["ghost"]; f.Status != Fail {
		t.Errorf("a node with no services must fail, got: %s", f)
	}
	if !r.Failed() {
		t.Error("result does not report failure")
	}
	if f := found["pinned spot checks"]; f.Status != Skip {
		t.Errorf("an unpinned network must skip the spot checks, got: %s", f)
	}
}

func TestCheckPlacementFindsUnownedAndContestedServices(t *testing.T) {
	repo := t.TempDir()
	gc := knopath.ConfigsDir(repo, "n")
	if err := os.MkdirAll(gc, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(gc, n+"-pq-mixserver.toml"), []byte("A = 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	topo := map[string]bool{"a-pq-mixserver": true, "b-pq-mixserver": true}

	r := checkPlacement(repo, "n", []string{"one", "two"},
		map[string][]string{"one": {"a"}, "two": {"a"}}, topo)
	if !r.Failed() {
		t.Fatalf("want failures, got %v", r.Findings)
	}
	found := bySubject(t, r)
	if f := found["placement a-pq-mixserver"]; f.Status != Fail || !strings.Contains(f.Detail, "one two") {
		t.Errorf("a service claimed twice must name both groups: %s", f)
	}
	if f := found["placement b-pq-mixserver"]; f.Status != Fail {
		t.Errorf("a service no group claims must fail: %s", f)
	}

	ok := checkPlacement(repo, "n", []string{"one", "two"},
		map[string][]string{"one": {"a"}, "two": {"b"}}, topo)
	if ok.Failed() || len(ok.Findings) != 1 || ok.Findings[0].Status != Pass {
		t.Fatalf("want a single pass, got %v", ok.Findings)
	}
}

func TestOperatorReportsPlacement(t *testing.T) {
	repo := t.TempDir()
	net := filepath.Join(repo, "networks", "fake")
	if err := os.MkdirAll(knopath.ConfigsIn(net), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(p, body string) {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []string{"alpha", "beta"} {
		write(filepath.Join(knopath.ConfigsIn(net), n+"-pq-mixserver.toml"), "A = 1\n")
	}

	write(knopath.NetworkFileIn(knopath.GeneratedIn(net)),
		"default = \"amd64\"\ndefault_os = \"debian-12\"\n"+
			"[operators.one]\nnodes = [\"alpha\"]\n[operators.two]\nnodes = [\"alpha\"]\n")

	r, err := Operator(OperatorOptions{Repo: repo, Network: "fake"})
	if err != nil {
		t.Fatal(err)
	}

	placement := map[string]Finding{}
	for _, f := range r.Findings {
		if strings.HasPrefix(f.Subject, "placement ") {
			placement[f.Subject] = f
		}
	}
	if f := placement["placement alpha-pq-mixserver"]; f.Status != Fail {
		t.Errorf("a service two groups claim must fail: %s", f)
	}
	if f := placement["placement beta-pq-mixserver"]; f.Status != Fail {
		t.Errorf("a service no group claims must fail: %s", f)
	}
}

func TestOperatorRejectsUnknownGroup(t *testing.T) {
	repo := testutil.RepoRoot(t)
	if _, err := Operator(OperatorOptions{Repo: repo, Network: "namenlos", Operators: []string{"nobody"}}); err == nil {
		t.Fatal("an unknown operator group must be an error, not an empty pass")
	}
}
