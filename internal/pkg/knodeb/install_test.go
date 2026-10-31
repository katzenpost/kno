package knodeb

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const breakHost = `#!/bin/sh
set -eu
mkdir -p /usr/local/bin /usr/local/lib/kno /etc/cron.d /etc/systemd/system
printf '#!/bin/sh\n. /usr/local/lib/kno/common.sh\n' > /usr/local/bin/kno-update
printf '#!/bin/sh\n' > /usr/local/bin/kno-push
printf '#!/bin/sh\n' > /usr/local/bin/kno-activate
chmod 0755 /usr/local/bin/kno-update /usr/local/bin/kno-push /usr/local/bin/kno-activate
printf '# the shared library\n' > /usr/local/lib/kno/common.sh
printf '# kno-update periodic check\n*/30 * * * * kno /usr/local/bin/kno-update >/dev/null 2>&1\n' > /etc/cron.d/kno-update
printf '[Service]\nExecStart=/usr/local/bin/kno-update\n' > /etc/systemd/system/kno-update.service
`

const checkScript = `#!/bin/sh
set -u
fail=0
ck() { if eval "$2"; then echo "  ok    $1"; else echo "  FAIL  $1"; fail=1; fi; }

if [ "${BREAK:-0}" = 1 ]; then
	echo "== seeding the host with the superseded layout =="
	sh /d/break.sh
fi

dpkg -i --force-depends /d/kno.deb || fail=1

echo "== the host kno prepared =="
ck "kno on PATH"                "command -v kno >/dev/null"
ck "updater user exists"        "id kno >/dev/null 2>&1"
ck "home dir"                   "[ -d /var/lib/kno-updater ]"
ck "releases dir"               "[ -d /var/lib/kno-updater/releases ]"
ck "state dir"                  "[ -d /var/lib/kno-updater/state ]"
ck "system config dir"          "[ -d /etc/katzenpost ]"
ck "no sudoers drop-in"         "! [ -e /etc/sudoers.d/kno-activate ]"
ck "no timer started"           "! systemctl is-enabled kno-update.timer 2>/dev/null | grep -q enabled"
ck "no network access used"     "[ ! -d /var/lib/kno-updater/katzenpost ]"

echo "== nothing superseded left behind =="
ck "no kno-update on PATH"      "[ ! -e /usr/local/bin/kno-update ]"
ck "no kno-push on PATH"        "[ ! -e /usr/local/bin/kno-push ]"
ck "no kno-activate on PATH"    "[ ! -e /usr/local/bin/kno-activate ]"
ck "no shell library"           "[ ! -e /usr/local/lib/kno/common.sh ]"
ck "unit calls 'kno update'"    "! [ -f /etc/systemd/system/kno-update.service ] || grep -q 'ExecStart=/usr/bin/kno update' /etc/systemd/system/kno-update.service"
ck "unit drops the old script"  "! [ -f /etc/systemd/system/kno-update.service ] || ! grep -q 'bin/kno-update' /etc/systemd/system/kno-update.service"
ck "cron calls 'kno update'"    "! [ -f /etc/cron.d/kno-update ] || grep -q '/usr/bin/kno update' /etc/cron.d/kno-update"
ck "cron drops the old script"  "! [ -f /etc/cron.d/kno-update ] || ! grep -q 'bin/kno-update' /etc/cron.d/kno-update"

printf 'kno-host-config kno-host-config/this_host boolean true\nkno-host-config kno-host-config/this_node select %s\n' "$NODE" | debconf-set-selections
DEBIAN_FRONTEND=noninteractive dpkg -i --force-depends /d/host-config.deb || fail=1
ck "kno.toml written"           "[ -f /etc/katzenpost/kno.toml ]"
ck "kno runs"                   "kno backup-keys --help >/dev/null 2>&1"
ck "installed CLI runs from home without source" "cd /root && kno status >/tmp/kno-status 2>&1"
ck "host configuration selects the requested node" "grep -Eq 'nodes: +$NODE' /tmp/kno-status"
ck "status requires no source checkout or Go installation" "! [ -d /root/network-organizer ] && ! [ -d /root/katzenpost ] && ! command -v go >/dev/null 2>&1"

echo "RESULT fail=$fail"
`

func TestPackagesBootstrapAHost(t *testing.T) {
	if os.Getenv("KNO_DEB_INSTALL_TEST") == "" {
		t.Skip("set KNO_DEB_INSTALL_TEST=1 to run the container install test")
	}
	podman, err := exec.LookPath("podman")
	if err != nil {
		t.Skip("podman not available")
	}
	knoDeb, hostDeb := os.Getenv("KNO_DEB"), os.Getenv("KNO_HOST_CONFIG_DEB")
	if knoDeb == "" || hostDeb == "" {
		t.Skip("set KNO_DEB and KNO_HOST_CONFIG_DEB to the built packages")
	}

	dir := t.TempDir()
	for _, f := range [][2]string{{knoDeb, "kno.deb"}, {hostDeb, "host-config.deb"}} {
		b, err := os.ReadFile(f[0])
		if err != nil {
			t.Fatalf("%s: %v", f[0], err)
		}
		if err := os.WriteFile(filepath.Join(dir, f[1]), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range [][2]string{{"check.sh", checkScript}, {"break.sh", breakHost}} {
		if err := os.WriteFile(filepath.Join(dir, f[0]), []byte(f[1]), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	for _, c := range []struct{ name, brk, node string }{
		{"desktop-built package on firefly without source", "0", "firefly"},
		{"desktop-built package on tortuna without source", "0", "tortuna"},
		{"upgrade over the superseded layout", "1", "mixy"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			out, err := exec.Command(podman, "run", "--rm", "--network=none",
				"-e", "BREAK="+c.brk, "-e", "NODE="+c.node, "-v", dir+":/d:ro,z",
				"debian:bookworm-slim", "sh", "/d/check.sh").CombinedOutput()
			t.Logf("container output:\n%s", out)
			if err != nil {
				t.Fatalf("podman run: %v", err)
			}
			if !strings.Contains(string(out), "RESULT fail=0") {
				t.Errorf("the installed host is not correctly prepared")
			}

			if n := strings.Count(string(out), "  ok    "); n < 21 {
				t.Errorf("only %d assertions ran; the check script did not complete", n)
			}
		})
	}
}
