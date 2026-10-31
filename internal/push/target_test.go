package push

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

func TestKatzenpostOnlyPushDoesNotRequireOrInstallKno(t *testing.T) {
	rel := staged(t,
		"katzenpost_0.0.104+ubuntu26.04_ppc64el.deb",
		"namenlos-config-annares-pq-authority_0.0.104_ppc64el.deb")
	if err := os.Remove(filepath.Join(rel, "pkgs", knoPkg)); err != nil {
		t.Fatal(err)
	}
	o := opts(t, rel, ModeFull, "annares-pq-authority")
	o.SkipKno = true
	p, err := PlanFor(o)
	if err != nil {
		t.Fatal(err)
	}
	assertKatzenpostOnly(t, p, "kno activate")
}

func TestKatzenpostOnlyFreeBSDPushKeepsTheManagementPackage(t *testing.T) {
	rel := freebsdRelease(t,
		"pkgs/katzenpost-0.0.107.pkg",
		"pkgs/namenlos-config-respectmy-pq-authority-0.0.107.pkg",
		"manifest/respectmy-pq-authority.conf")
	o := Options{
		Repo: testutil.RepoRoot(t), Network: "namenlos",
		Label: "jailhost", Node: "root@vm", Tag: "v0.0.107", Rel: rel, Mode: ModeInstall,
		Services: []string{"respectmy-pq-authority"}, SSH: "ssh", SCP: "scp", SkipKno: true,
	}
	p, err := PlanFor(o)
	if err != nil {
		t.Fatal(err)
	}
	assertKatzenpostOnly(t, p, "/usr/local/bin/kno activate")
}

func assertKatzenpostOnly(t *testing.T, p Plan, activate string) {
	t.Helper()
	if len(p.Packages) != 2 {
		t.Fatalf("wrong package scope: %v", p.Packages)
	}
	commands := strings.Join(p.Commands(), "\n")
	if !strings.Contains(commands, activate) {
		t.Fatalf("no activation: %s", commands)
	}
	for _, forbidden := range []string{"kno_", "kno-host-config", "kno-0", "dpkg -i", "pkg add -f", "bootstrap"} {
		if strings.Contains(commands, forbidden) {
			t.Fatalf("Katzenpost-only push includes %s: %s", forbidden, commands)
		}
	}
}
