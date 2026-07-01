package freebsd

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/katzenpost/kno/internal/sign"
)

func TestEveryPackageKeepsItsBytes(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1600000000")
	t.Setenv("PKG_MAINTAINER", "")
	t.Setenv("FREEBSD_RCD", "")
	write := func(p, body string, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	repo := t.TempDir()
	write(filepath.Join(repo, "packaging/freebsd/rc.d/katzenpost"), "#!/bin/sh\n", 0o755)
	write(filepath.Join(repo, "packaging/freebsd/rc.d/kpclientd"), "#!/bin/sh\n", 0o644)
	write(sign.RepoPath(repo, "namenlos"), "op ssh-ed25519 AAAA\n", 0o644)
	bindir := filepath.Join(t.TempDir(), "bin")
	fakeBins(t, bindir)
	rel := mkRelease(t)
	write(filepath.Join(rel, "client.toml"), "[Listen]\n  Address = \"@katzenpost\"\n", 0o600)
	write(filepath.Join(rel, "thinclient.toml"), "Address = \"@katzenpost\"\n", 0o644)
	kno := filepath.Join(t.TempDir(), "kno")
	write(kno, "KNO\n", 0o755)

	build := map[string]func() (string, error){
		"katzenpost": func() (string, error) { return Package(bindir, rel, "v0.0.107", "3", repo, "") },
		"config": func() (string, error) {
			return PackageConfig(rel, "v0.0.107", "3", "x-pq-mixserver", Svc{
				Build:        "server",
				ConfigSrc:    "configs/x-pq-mixserver.toml",
				ConfigDst:    "/usr/local/etc/katzenpost/x-pq-mixserver.toml",
				StateDir:     "/var/lib/pq-katzenpost-mixserver",
				KeysSrc:      []string{"keys/mix-id-keys"},
				PrestartHook: "rm -f /var/lib/pq-katzenpost-mixserver/persistence.db",
				Stamp:        "+20261020.gabc",
			}, repo, DefaultABI)
		},
		"client": func() (string, error) { return PackageClient(rel, "v0.0.107", "3", repo, DefaultABI) },
		"kno": func() (string, error) {
			return KnoPackage(kno, "0.0.107-1+20261020123335.g4eca74c", t.TempDir(), "", 1600000000)
		},
		"host-config": func() (string, error) {
			return HostConfigPackage(freebsdHostConfig(), "jailhost", "0.0.107+20261020.gdef",
				t.TempDir(), "", repo, "namenlos", 1600000000)
		},
	}
	want := map[string]string{
		"katzenpost": "katzenpost-0.0.107_3.pkg" +
			" b1052d3573a3d9059d0c603c9521ff90f40136e6eb11f44d4a4ec43cbd27c81d",
		"config": "namenlos-config-x-pq-mixserver-0.0.107.20261020.gabc_3.pkg" +
			" 7d57199dc43e92cb11b824e131fc56b409a24d2e99bd087d37143fb279fd3601",
		"client": "namenlos-config-kpclientd-0.0.107_3.pkg" +
			" 6ce662fdbbf2db2c34c5493010e121c7453dc50c21a81a468a6e43f04aa1e598",
		"kno": "kno-0.0.107.20261020123335.g4eca74c_1.pkg" +
			" 1638aaa5725daf39b82a09bfcd0a715a6348534b11cb03ebf4547acdf79e686c",
		"host-config": "kno-host-config-jailhost-0.0.107.20261020.gdef.pkg" +
			" c997d0f3e05d4ef8d6a2c1bd7687544af6def9d020992ad8cdbb8826e9dd8850",
	}
	for name, f := range build {
		p, err := f()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		if got := filepath.Base(p) + " " + hex.EncodeToString(sum[:]); got != want[name] {
			t.Errorf("%s: %s, want %s", name, got, want[name])
		}
	}
}
