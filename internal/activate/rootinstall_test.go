package activate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnUpdaterMadeKnoDebIsNotInstalledAsRoot(t *testing.T) {
	if _, err := exec.LookPath("dpkg-deb"); err != nil {
		t.Skip("no dpkg-deb")
	}
	root := filepath.Join(t.TempDir(), "pkg")
	os.MkdirAll(filepath.Join(root, "DEBIAN"), 0o755)
	os.WriteFile(filepath.Join(root, "DEBIAN", "control"), []byte(
		"Package: kno\nVersion: 9.9.9\nArchitecture: all\nMaintainer: x <x@x>\nDescription: x\n"), 0o644)
	os.WriteFile(filepath.Join(root, "DEBIAN", "postinst"), []byte("#!/bin/sh\nid > /tmp/owned\n"), 0o755)
	deb := filepath.Join(t.TempDir(), "kno_9.9.9_all.deb")
	if b, err := exec.Command("dpkg-deb", "--root-owner-group", "--build", root, deb).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, b)
	}
	if _, err := knoInstallPlan([]string{deb}, false); err != nil {
		t.Errorf("without require_signatures a kno deb is refused: %v", err)
	}
	if _, err := knoInstallPlan([]string{deb}, true); err == nil {
		t.Errorf("a kno deb with a postinst, from a directory the updater writes, is planned for dpkg -i as root")
	}
}

func TestARestartUnitCannotBeAnOption(t *testing.T) {
	for _, s := range restartSteps([]string{"--root=/tmp", "-H", "kpserver@mixy.service"}) {
		for _, a := range s.Argv[3:] {
			if strings.HasPrefix(a, "-") {
				t.Errorf("systemctl gets the option %q from --restart", a)
			}
		}
	}
}
