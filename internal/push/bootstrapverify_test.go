package push

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/build"
)

func TestBootstrapVerifiesBeforeInstalling(t *testing.T) {
	cmd := bootstrapCmd("/rel", []string{"kno_1_amd64.deb", "kno-host-config_1_all.deb"})

	install := strings.Index(cmd, "dpkg -i")
	if install < 0 {
		t.Fatal("no install in the bootstrap command")
	}
	for _, want := range []string{"allowed_signers", "ssh-keygen", "kno-release", "sha256sum"} {
		at := strings.Index(cmd, want)
		if at < 0 {
			t.Errorf("bootstrap does not mention %q:\n%s", want, cmd)
			continue
		}
		if at > install {
			t.Errorf("%q happens AFTER dpkg -i, which is too late:\n%s", want, cmd)
		}
	}
}

func TestBootstrapStillWorksWithNoAllowlist(t *testing.T) {
	cmd := bootstrapCmd("/rel", []string{"kno_1_amd64.deb"})
	if !strings.Contains(cmd, "-f /etc/katzenpost/allowed_signers ]") &&
		!strings.Contains(cmd, "allowed_signers") {
		t.Fatal("the allowlist is not consulted at all")
	}
	if !strings.Contains(cmd, "dpkg -i") {
		t.Error("no install step remains")
	}
}

func TestBootstrapRefusesAPackageTheSumsDoNotList(t *testing.T) {
	for _, tc := range []struct {
		listed bool
		uid    string
	}{{false, "1000"}, {true, "1000"}, {false, "0"}, {true, "0"}} {
		rel := t.TempDir()
		pkgs := filepath.Join(rel, "pkgs")
		bin := filepath.Join(rel, "bin")
		marker := filepath.Join(rel, "installed")
		sudoed := filepath.Join(rel, "sudoed")
		for _, d := range []string{pkgs, bin} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		deb := []byte("a kno package")
		if err := os.WriteFile(filepath.Join(pkgs, "kno_1_amd64.deb"), deb, 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(deb)
		name := "other_1_amd64.deb"
		if tc.listed {
			name = "kno_1_amd64.deb"
		}
		line := hex.EncodeToString(sum[:]) + "  " + name + "\n"
		cfg := []byte("a host config package")
		if err := os.WriteFile(filepath.Join(pkgs, "kno-host-config_1_all.deb"), cfg, 0o644); err != nil {
			t.Fatal(err)
		}
		cfgSum := sha256.Sum256(cfg)
		line += hex.EncodeToString(cfgSum[:]) + "  kno-host-config_1_all.deb\n"
		if err := os.WriteFile(filepath.Join(pkgs, build.SumsName), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
		for f, body := range map[string]string{
			"id":   "echo " + tc.uid,
			"sudo": "touch " + sudoed + "; exec env \"$@\"",
			"dpkg": "touch " + marker,
			"kno":  "",
		} {
			if err := os.WriteFile(filepath.Join(bin, f), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		c := exec.Command("sh", "-c", bootstrapCmd(rel, []string{"kno_1_amd64.deb", "kno-host-config_1_all.deb"}))
		c.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
		out, err := c.CombinedOutput()
		_, statErr := os.Stat(marker)
		installed := statErr == nil
		_, statErr = os.Stat(sudoed)
		if root := tc.uid == "0"; installed && root == (statErr == nil) {
			t.Errorf("uid %s: sudo ran = %v; root needs no sudo, a user does:\n%s", tc.uid, statErr == nil, out)
		}
		if tc.listed && (err != nil || !installed) {
			t.Errorf("uid %s: a listed package was not installed: %v\n%s", tc.uid, err, out)
		}
		if !tc.listed && (err == nil || installed) {
			t.Errorf("uid %s: a package the sums do not list was installed:\n%s", tc.uid, out)
		}
	}
}
