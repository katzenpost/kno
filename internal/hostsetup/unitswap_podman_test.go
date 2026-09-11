package hostsetup_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	trapUnit = "knotrap.service"
	trapWho  = "/run/knotrap/who"
)

const packagedUnit = `[Unit]
Description=kno unit-swap rehearsal

[Service]
Type=oneshot
RemainAfterExit=yes
DynamicUser=yes
# /run is root-owned, so a service running as anything else cannot write
# there. RuntimeDirectory makes systemd create /run/knotrap owned by whoever
# the unit runs as -- which is also how the real generate unit gets the
# XDG_RUNTIME_DIR rootless podman needs.
RuntimeDirectory=knotrap
ExecStart=/bin/sh -c 'id -un > ` + trapWho + `'

[Install]
WantedBy=multi-user.target
`

var overrideUnit = strings.Replace(packagedUnit, "DynamicUser=yes",
	"User=knotrapper\nGroup=knotrapper", 1)

func TestDroppingTheUnitOverrideHandsTheServiceBack(t *testing.T) {
	podman, err := exec.LookPath("podman")
	if err != nil {
		t.Skip("podman not available")
	}
	if _, err := exec.LookPath("dpkg-deb"); err != nil {
		t.Skip("dpkg-deb not available")
	}
	img := buildImage(t, podman)
	c := startPlain(t, podman, img, "kno-unitswap")

	p := &probe{t: t}

	base := t.TempDir()
	unitDeb(t, base, "knotrap-base", "1.0",
		map[string]string{"lib/systemd/system/" + trapUnit: packagedUnit})

	host1 := t.TempDir()
	unitDeb(t, host1, "knotrap-host", "1.0",
		map[string]string{"etc/systemd/system/" + trapUnit: overrideUnit})

	host2 := t.TempDir()
	unitDeb(t, host2, "knotrap-host", "2.0", nil)

	for _, d := range []string{base, host1, host2} {
		if out, err := exec.Command(podman, "cp", d+"/.", c.name+":/debs").CombinedOutput(); err != nil {
			t.Fatalf("podman cp: %v\n%s", err, out)
		}
	}

	p.section("1. the host names an account, so the override is what runs")
	out, err := c.execSh("useradd --system --shell /usr/sbin/nologin knotrapper" +
		" && dpkg -i /debs/knotrap-base_1.0_all.deb /debs/knotrap-host_1.0_all.deb")
	p.ck("both packages install", err == nil, out)
	out, err = c.exec("systemctl", "enable", "--now", trapUnit)
	p.ck("the unit enables and starts", err == nil, out)

	p.sh(c, "systemd reads the override under /etc",
		"systemctl show "+trapUnit+" -p FragmentPath | grep -qx FragmentPath=/etc/systemd/system/"+trapUnit)
	p.sh(c, "and it ran as the named account", "grep -qx knotrapper "+trapWho)

	p.sh(c, "the enable symlink names the file about to be removed",
		"test \"$(readlink /etc/systemd/system/multi-user.target.wants/"+trapUnit+
			")\" = /etc/systemd/system/"+trapUnit)
	p.sh(c, "the override is a regular file, not a conffile",
		"! grep -q "+trapUnit+" /var/lib/dpkg/info/knotrap-host.conffiles 2>/dev/null")

	p.section("2. the upgrade that stops shipping it")
	out, err = c.execSh("dpkg -i --force-confdef --force-confold --force-confmiss" +
		" /debs/knotrap-host_2.0_all.deb")
	p.ck("the upgrade installs", err == nil, out)

	p.sh(c, "dpkg removed the override", "! test -e /etc/systemd/system/"+trapUnit)
	p.sh(c, "leaving the enable symlink dangling",
		"test -L /etc/systemd/system/multi-user.target.wants/"+trapUnit+
			" && ! test -e /etc/systemd/system/multi-user.target.wants/"+trapUnit)

	out, err = c.exec("systemctl", "daemon-reload")
	p.ck("daemon-reload is clean", err == nil, out)

	p.section("3. what systemd does with a dangling want")

	p.sh(c, "systemd falls back to the packaged unit under /lib",
		"systemctl show "+trapUnit+" -p FragmentPath | grep -qx FragmentPath=/lib/systemd/system/"+trapUnit)
	p.sh(c, "the unit is still enabled", "systemctl is-enabled "+trapUnit)
	p.sh(c, "multi-user.target still wants it",
		"systemctl list-dependencies multi-user.target | grep -q "+trapUnit)

	p.sh(c, "the unit now asks for a transient account",
		"systemctl show "+trapUnit+" -p DynamicUser | grep -qx DynamicUser=yes")

	p.sh(c, "and no longer the static account",
		"! systemctl show "+trapUnit+" -p User | grep -q knotrapper")
	p.sh(c, "the named account it used to run as is gone from the unit",
		"! systemctl cat "+trapUnit+" | grep -q knotrapper")
	p.t.Log("not proven here: that a DynamicUser unit STARTS. Rootless podman" +
		" has no mount namespacing, which DynamicUser requires.")

	p.reportAtLeast(16)
}

func startPlain(t *testing.T, podman, img, name string) *box {
	t.Helper()
	name = name + "-" + fmt.Sprint(os.Getpid())
	_ = exec.Command(podman, "rm", "-f", name).Run()
	args := []string{"run", "-d", "--name", name, "--systemd=always",
		"--network=none", img, "/sbin/init"}
	if out, err := exec.Command(podman, args...).CombinedOutput(); err != nil {
		t.Skipf("could not start a systemd container: %v\n%s", err, out)
	}
	b := &box{t: t, podman: podman, name: name}
	t.Cleanup(func() {
		if t.Failed() {
			out, _ := exec.Command(podman, "logs", name).CombinedOutput()
			t.Logf("container logs:\n%s", out)
		}
		_ = exec.Command(podman, "rm", "-f", name).Run()
	})
	b.waitForSystemd()
	if _, err := b.exec("mkdir", "-p", "/debs"); err != nil {
		t.Fatal(err)
	}
	return b
}

func unitDeb(t *testing.T, dir, name, version string, files map[string]string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), name+version)
	if err := os.MkdirAll(filepath.Join(root, "DEBIAN"), 0o755); err != nil {
		t.Fatal(err)
	}
	control := fmt.Sprintf("Package: %s\nVersion: %s\nArchitecture: all\n"+
		"Maintainer: kno test <test@invalid>\nDescription: unit-swap rehearsal\n", name, version)
	if err := os.WriteFile(filepath.Join(root, "DEBIAN", "control"), []byte(control), 0o644); err != nil {
		t.Fatal(err)
	}
	for p, body := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(dir, name+"_"+version+"_all.deb")
	if b, err := exec.Command("dpkg-deb", "--build", root, out).CombinedOutput(); err != nil {
		t.Skipf("could not build a test package: %v\n%s", err, b)
	}
}
