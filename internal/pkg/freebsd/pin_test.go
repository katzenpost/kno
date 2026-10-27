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
				Build:     "server",
				ConfigSrc: "configs/x-pq-mixserver.toml",
				ConfigDst: "/usr/local/etc/katzenpost/x-pq-mixserver.toml",
				StateDir:  "/var/lib/pq-katzenpost-mixserver",
				KeysSrc:   []string{"keys/mix-id-keys"},
				Stamp:     "+20261020.gabc",
			}, repo, DefaultABI, nil)
		},
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
			" 365ee4498954a3f470af69a71f09698d8c6f75a880dd70c682d9e2294ef1790b",
		"kno": "kno-0.0.107.20261020123335_1.pkg" +
			" 91ce6162cf443a6481e291ac7b912cab8b18d655c9feaf7ceed84e4ee2b660e6",
		"host-config": "kno-host-config-jailhost-0.0.107.20261020.gdef.pkg" +
			" bedb0822eabd466818e6d4bc25435474b0938912cb0a2c4d6a5f0898d99e6177",
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
