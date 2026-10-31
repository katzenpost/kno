package hostconfig

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/testutil"
)

func TestLillyNetworkUpgradePreservesOldFilesAndRefreshesAllConsumers(t *testing.T) {
	root := t.TempDir()
	repo := testutil.RepoRoot(t)
	for _, name := range []string{"client.toml", "thinclient.toml", "authority.toml"} {
		p := filepath.Join(root, "etc/katzenpost", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("old windfallgw "+name), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	key := filepath.Join(root, "identity.private.pem")
	if err := os.WriteFile(key, []byte("existing private key"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := filepath.Join(root, "etc/katzenpost/kno/client.toml")
	if err := os.MkdirAll(filepath.Dir(client), 0o755); err != nil {
		t.Fatal(err)
	}
	generated, err := os.ReadFile(filepath.Join(knopath.ConfigsDir(repo, "namenlos"), "client.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(client, generated, 0o644); err != nil {
		t.Fatal(err)
	}
	migration, err := networkFiles(root, repo, "namenlos", []string{"lilly-pq-servicenode"})
	if err != nil {
		t.Fatal(err)
	}
	script := strings.ReplaceAll(migration, "/etc/", root+"/etc/")
	script = strings.ReplaceAll(script, "/var/backups/", root+"/var/backups/")
	run := func() {
		t.Helper()
		if out, err := exec.Command("sh", "-ec", script).CombinedOutput(); err != nil {
			t.Fatalf("upgrade: %v\n%s", err, out)
		}
	}
	run()
	for _, name := range []string{"client.toml", "thinclient.toml", "authority.toml"} {
		p := filepath.Join(root, "etc/katzenpost", name)
		got, err := os.ReadFile(p)
		if err != nil || len(got) == 0 || strings.Contains(string(got), "windfallgw") {
			t.Fatalf("legacy consumer %s still has stale configuration: %v", name, err)
		}
		backups, err := filepath.Glob(filepath.Join(root, "var/backups/kno/network-configs/saved.*", p))
		if err != nil || len(backups) != 1 {
			t.Fatalf("%s backup paths: %v (%v)", name, backups, err)
		}
		old, err := os.ReadFile(backups[0])
		if err != nil || string(old) != "old windfallgw "+name {
			t.Fatalf("%s original was lost: %q (%v)", name, old, err)
		}
	}
	if err := os.WriteFile(client, []byte("next generated client"), 0o644); err != nil {
		t.Fatal(err)
	}
	run()
	got, err := os.ReadFile(filepath.Join(root, "etc/katzenpost/client.toml"))
	if err != nil || string(got) != "next generated client" {
		t.Fatalf("second upgrade left a stale client: %q (%v)", got, err)
	}
	if b, err := os.ReadFile(key); err != nil || string(b) != "existing private key" {
		t.Fatalf("private key changed: %q (%v)", b, err)
	}
	backups, _ := filepath.Glob(filepath.Join(root, "var/backups/kno/network-configs/saved.*"))
	if len(backups) != 3 {
		t.Fatalf("unchanged aliases created redundant backups: %v", backups)
	}
}

func TestAuthorityHostKeepsItsLiveAuthorityConfiguration(t *testing.T) {
	root := t.TempDir()
	repo := testutil.RepoRoot(t)
	migration, err := networkFiles(root, repo, "namenlos", []string{"respectmy-pq-authority"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(migration, "link_network_config /etc/katzenpost/authority.toml") {
		t.Fatal("authority host would replace its live authority configuration")
	}
	got, err := os.ReadFile(filepath.Join(root, managedNetworkDir, "authority.toml"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(knopath.ConfigsDir(repo, "namenlos"), "respectmy-pq-authority.toml"))
	if err != nil || string(got) != string(want) {
		t.Fatal("managed authority reference is not this host's authority")
	}
}

func TestPackagedLillyUpgradeRefreshesLegacyStatusInputs(t *testing.T) {
	if os.Getenv("KNO_DEB_INSTALL_TEST") == "" {
		t.Skip("set KNO_DEB_INSTALL_TEST=1 for the existing container installation suite")
	}
	podman, err := exec.LookPath("podman")
	if err != nil {
		t.Skip("podman not available")
	}
	dir := t.TempDir()
	c := config.Config{Network: "namenlos", Hosts: map[string]config.Host{
		"lilly": {Kpclientd: config.Bool(true), Services: []string{"lilly-pq-servicenode"}},
	}}
	pkg, err := PackageForHost(t.Context(), c, "lilly", "0.0.107-2", dir, testutil.RepoRoot(t), "namenlos", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	script := `set -eu
mkdir -p /old/DEBIAN /old/etc/katzenpost /old/etc/kpclientd
printf 'Package: katzenpost\nVersion: 0.0.107-1\nArchitecture: all\nMaintainer: test <test@example.org>\nDescription: old client defaults\n' > /old/DEBIAN/control
for p in /etc/katzenpost/client.toml /etc/katzenpost/thinclient.toml /etc/kpclientd/client.toml; do
  printf 'old windfallgw\n' > "/old$p"
  printf '%s\n' "$p" >> /old/DEBIAN/conffiles
done
dpkg-deb --build /old /old.deb
dpkg -i /old.deb
printf 'old authority\n' > /etc/katzenpost/authority.toml
mkdir -p /var/lib/katzenpost/authority
printf 'private identity\n' > /var/lib/katzenpost/authority/identity.private.pem
dpkg -i --force-confold --force-confdef /d/host.deb
for n in client thinclient authority; do
  test -L "/etc/katzenpost/$n.toml"
  test -s "/etc/katzenpost/$n.toml"
  ! grep -q windfallgw "/etc/katzenpost/$n.toml"
done
grep -q '\[\[Authorities\]\]' /etc/katzenpost/authority.toml
grep -q '\[SphinxGeometry\]' /etc/katzenpost/authority.toml
grep -q '\[\[Mixes\]\]' /etc/katzenpost/authority.toml
find /var/backups/kno/network-configs -type f | sort > /tmp/before
[ "$(wc -l < /tmp/before)" -eq 4 ]
[ "$(grep -rl 'old windfallgw' /var/backups/kno/network-configs | wc -l)" -eq 3 ]
[ "$(grep -rl 'old authority' /var/backups/kno/network-configs | wc -l)" -eq 1 ]
dpkg -i --force-confold --force-confdef /old.deb
dpkg -i --force-confold --force-confdef /d/host.deb
find /var/backups/kno/network-configs -type f | sort > /tmp/after
cmp /tmp/before /tmp/after
grep -qx 'private identity' /var/lib/katzenpost/authority/identity.private.pem
! grep -q windfallgw /etc/katzenpost/client.toml
printf 'LILLY_UPGRADE_PASS\n'
`
	b, err := os.ReadFile(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "host.deb"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "upgrade.sh"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(podman, "run", "--rm", "--network=none", "-v", dir+":/d:ro,Z", "debian:bookworm-slim", "sh", "/d/upgrade.sh").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "LILLY_UPGRADE_PASS") {
		t.Fatalf("packaged lilly upgrade: %v\n%s", err, out)
	}
}
