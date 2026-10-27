package build

import (
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

func TestNodeServicesNeedsNoHostEntry(t *testing.T) {
	got, err := nodeServices(testutil.RepoRoot(t), "namenlos", []string{"mixy"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "mixy-pq-mixserver" {
		t.Errorf("nodeServices(mixy) = %v, want mixy-pq-mixserver", got)
	}
}

func TestNodeServicesRejectsAnUnknownNode(t *testing.T) {
	_, err := nodeServices(testutil.RepoRoot(t), "namenlos", []string{"nosuch"})
	if err == nil || !strings.Contains(err.Error(), "nosuch") {
		t.Errorf("err = %v, want one that names nosuch", err)
	}
}

func TestOnlyOneScopeIsAllowed(t *testing.T) {
	for _, v := range []flags{
		{allNodes: true, group: "annares"},
		{node: "mixy", host: "mixy"},
		{node: "mixy", svcList: "mixy-pq-mixserver"},
		{allNodes: true, allHosts: true},
	} {
		if err := oneScope(v); err == nil {
			t.Errorf("%+v was accepted", v)
		}
	}
	if err := oneScope(flags{node: "mixy"}); err != nil {
		t.Errorf("one scope was refused: %v", err)
	}
}

func TestOnlyADebBackendGetsAConfigDeb(t *testing.T) {
	repo := testutil.RepoRoot(t)
	for svc, want := range map[string]bool{
		"mixy-pq-mixserver":         true,
		"allmixedup-pq-gatewaynode": false,
		"wauland-pq-mixserver":      false,
	} {
		if got := debService(repo, "namenlos", svc); got != want {
			t.Errorf("debService(%s) = %v, want %v", svc, got, want)
		}
	}
}
