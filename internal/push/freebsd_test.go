package push

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

func freebsdRelease(t *testing.T, names ...string) string {
	t.Helper()
	rel := t.TempDir()
	for _, n := range names {
		touch(t, filepath.Join(rel, n))
	}
	return rel
}

func TestPushToAFreeBSDHostShipsPkgsAndActivates(t *testing.T) {
	rel := freebsdRelease(t,
		"pkgs/katzenpost-0.0.107.pkg",
		"pkgs/namenlos-config-kpclientd-0.0.107.pkg",
		"pkgs/namenlos-config-respectmy-pq-authority-0.0.107.20261020.gabc.pkg",
		"pkgs/namenlos-config-allmixedup-pq-gatewaynode-0.0.107.20261020.gabc.pkg",
		"pkgs/kno-0.0.107.20261020.gdef.pkg",
		"pkgs/kno-host-config-jailhost-0.0.107.20261020.gdef.pkg",
		"pkgs/SHA256SUMS",
		"manifest/respectmy-pq-authority.conf",
		"manifest/allmixedup-pq-gatewaynode.conf",
	)
	o := Options{
		Repo: testutil.RepoRoot(t), Network: "namenlos",
		Label: "jailhost", Node: "root@vm", Tag: "v0.0.107", Rel: rel, Mode: ModeInstall,
		Services: []string{"respectmy-pq-authority", "allmixedup-pq-gatewaynode"},
		SSH:      "ssh", SCP: "scp",
	}
	p, err := PlanFor(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Packages) != 6 {
		t.Errorf("push ships %d packages, want 6: %v", len(p.Packages), baseNames(p.Packages))
	}
	all := strings.Join(p.Commands(), "\n")
	for _, want := range []string{
		"pkg add -f kno-0.0.107.20261020.gdef.pkg kno-host-config-jailhost-0.0.107.20261020.gdef.pkg",
		"sha256 -q",
		"ssh-keygen -Y verify -f /usr/local/etc/katzenpost/allowed_signers",
		"/usr/local/bin/kno activate .local/share/katzenpost/kno/pkgs/v0.0.107 v0.0.107",
		"manifest/respectmy-pq-authority.conf",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the push plan lacks %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "dpkg") || strings.Contains(all, "systemctl") {
		t.Errorf("a FreeBSD push runs Debian tools:\n%s", all)
	}
	for _, e := range BuildStep(o).Env {
		if e == "KNO_SKIP_FREEBSD=1" {
			t.Error("the build for a FreeBSD host skips the FreeBSD packages")
		}
	}
}

func TestPushToAFreeBSDHostNeedsTheKnoPackage(t *testing.T) {
	rel := freebsdRelease(t,
		"pkgs/katzenpost-0.0.107.pkg",
		"pkgs/namenlos-config-kpclientd-0.0.107.pkg",
		"pkgs/namenlos-config-respectmy-pq-authority-0.0.107.pkg",
		"manifest/respectmy-pq-authority.conf",
	)
	o := Options{
		Repo: testutil.RepoRoot(t), Network: "namenlos",
		Label: "jailhost", Node: "root@vm", Tag: "v0.0.107", Rel: rel, Mode: ModeInstall,
		Services: []string{"respectmy-pq-authority"}, SSH: "ssh", SCP: "scp",
	}
	if _, err := PlanFor(o); err == nil || !strings.Contains(err.Error(), "kno-<version>.pkg") {
		t.Fatalf("a push without the kno package was planned: %v", err)
	}
}

func TestCopyModeIsRefusedForAFreeBSDHost(t *testing.T) {
	rel := freebsdRelease(t, "manifest/respectmy-pq-authority.conf")
	o := Options{
		Repo: testutil.RepoRoot(t), Network: "namenlos",
		Label: "jailhost", Node: "root@vm", Tag: "v0.0.107", Rel: rel, Mode: ModeCopy,
		Services: []string{"respectmy-pq-authority"}, SSH: "ssh", SCP: "scp",
	}
	_, err := PlanFor(o)
	if err == nil || !strings.Contains(err.Error(), "freebsd-ezjail") || !strings.Contains(err.Error(), "respectmy-pq-authority") {
		t.Fatalf("copy mode to a jail host must be refused and say why: %v", err)
	}
}
