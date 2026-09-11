package doctor

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/peers"
	"github.com/katzenpost/kno/internal/service"
	"github.com/katzenpost/kno/internal/track"
)

func embeddedOnly(t *testing.T) string {
	t.Helper()
	t.Setenv("KNO_CACHE", t.TempDir())
	t.Setenv("KNO_REPO", "")
	t.Chdir(t.TempDir())
	repo, err := config.EmbeddedRepo()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(knopath.EditableDir(repo, "namenlos")); !os.IsNotExist(err) {
		t.Fatalf("the embedded tree carries %s; a host must work without it",
			knopath.EditableName)
	}
	return repo
}

func TestAHostWithOnlyTheBinaryResolvesItsServices(t *testing.T) {
	repo := embeddedOnly(t)
	for svc, want := range map[string][2]string{
		"wauland-pq-mixserver":     {"docker", "kp-mix"},
		"annares-pq-storagenode-0": {"systemd", "kpreplica@annares-pq-storagenode-0"},
		"respectmy-pq-authority":   {"freebsd-ezjail", "katzenpost"},
		"mixy-pq-mixserver":        {"systemd", "kpserver@mixy-pq-mixserver"},
	} {
		s, err := service.ResolveService(repo, "namenlos", svc)
		if err != nil {
			t.Fatal(err)
		}
		if s.Backend != want[0] || s.Unit != want[1] {
			t.Errorf("%s: backend %q unit %q, want %q %q", svc, s.Backend, s.Unit, want[0], want[1])
		}
	}
}

func TestAHostWithOnlyTheBinaryKnowsTheNetwork(t *testing.T) {
	repo := embeddedOnly(t)
	if a, o := config.NodeArch(repo, "namenlos", "annares"),
		config.NodeOS(repo, "namenlos", "annares"); a != "ppc64el" || o != "ubuntu-26.04" {
		t.Errorf("annares is %s on %s, want ppc64el on ubuntu-26.04", a, o)
	}
	if a := config.NodeArch(repo, "namenlos", "oneeyedman"); a != "arm64" {
		t.Errorf("oneeyedman is %s, want arm64", a)
	}
	if len(config.NetworkArches(repo, "namenlos")) < 3 {
		t.Errorf("arches %v; namenlos has amd64, arm64 and ppc64el",
			config.NetworkArches(repo, "namenlos"))
	}
	if ops := config.LoadOperators(repo, "namenlos"); len(ops["annares"]) == 0 {
		t.Errorf("operators %v; annares operates nodes", ops)
	}
	if p := peers.NodePeers(repo, "namenlos", "oneeyedman")["luscus"]; p == "" {
		t.Error("oneeyedman does not find luscus on its LAN")
	}
	if ids := recordedIdentities(repo, "namenlos"); ids["annares-pq-authority"] == "" {
		t.Errorf("no recorded identity for annares-pq-authority among %d", len(ids))
	}
}

func TestDoctorChecksThePackageDataWithOnlyTheBinary(t *testing.T) {
	repo := embeddedOnly(t)
	t.Setenv("KNO_REPO", repo)
	var b bytes.Buffer
	r := newResult(&b, true)
	checkPackage(r, track.Env{Repo: repo, Network: "namenlos"})
	if r.fails != 0 || !strings.Contains(b.String(), "distinct references") {
		t.Errorf("doctor found %d fault(s) in the embedded package data:\n%s", r.fails, b.String())
	}
}
