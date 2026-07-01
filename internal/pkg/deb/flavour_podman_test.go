package deb

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const swapScript = `#!/bin/sh
set -u
fail=0
ck() { if eval "$2"; then echo "  ok    $1"; else echo "  FAIL  $1"; fail=1; fi; }
inst() { dpkg-query -W -f='${Status}' "$1" 2>/dev/null | grep -q 'install ok installed'; }
export DEBIAN_FRONTEND=noninteractive
APT="apt-get -o Dir::Etc::sourcelist=/dev/null -o Dir::Etc::sourceparts=/dev/null"

echo "== the normal flavour =="
$APT install -y /d/normal.deb >/tmp/a 2>&1 || { cat /tmp/a; exit 1; }
ck "installed" "inst pkgflav"
ck "its file is the normal one" "grep -qx normal /etc/pkgflav/conf"

echo "== the warped flavour over it =="
$APT install -y /d/warped.deb >/tmp/b 2>&1 || { cat /tmp/b; exit 1; }
ck "warped installed" "inst pkgflav-warped"
ck "normal removed" "! inst pkgflav"
ck "its file replaced the normal one" "grep -qx warped /etc/pkgflav/conf"

echo "== and back =="
$APT install -y --allow-downgrades /d/normal.deb >/tmp/c 2>&1 || { cat /tmp/c; exit 1; }
ck "normal back" "inst pkgflav"
ck "warped removed" "! inst pkgflav-warped"
ck "file is the normal one again" "grep -qx normal /etc/pkgflav/conf"

echo "RESULT fail=$fail"
`

func TestFlavoursReplaceEachOther(t *testing.T) {
	podman, err := exec.LookPath("podman")
	if err != nil {
		t.Skip("podman not available")
	}
	if _, err := exec.LookPath("dpkg-deb"); err != nil {
		t.Skip("dpkg-deb not available")
	}
	dir := t.TempDir()
	buildFlavour(t, dir, false, "normal.deb")
	buildFlavour(t, dir, true, "warped.deb")
	if err := os.WriteFile(filepath.Join(dir, "t.sh"), []byte(swapScript), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(podman, "run", "--rm", "--network=none",
		"-v", dir+":/d:ro,Z", "debian:bookworm-slim", "sh", "/d/t.sh").CombinedOutput()
	t.Logf("container output:\n%s", out)
	if err != nil {
		t.Fatalf("podman run: %v", err)
	}
	if !strings.Contains(string(out), "RESULT fail=0") {
		t.Error("the flavours do not replace each other cleanly")
	}
	if n := strings.Count(string(out), "  ok    "); n < 8 {
		t.Errorf("only %d assertions ran; the script did not complete", n)
	}
}

func buildFlavour(t *testing.T, dir string, warped bool, out string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "pkg")
	for _, d := range []string{"DEBIAN", "etc/pkgflav"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	name := Flavoured("pkgflav", warped)
	body := "normal"
	if warped {
		body = "warped"
	}
	if err := os.WriteFile(filepath.Join(root, "etc/pkgflav/conf"), []byte(body+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	control := fmt.Sprintf("Package: %s\nVersion: 1.0\nArchitecture: all\n"+
		"Maintainer: t <t@example.invalid>\n%sDescription: flavour test\n",
		name, AlternativesFields("pkgflav", "1.0"))
	if err := os.WriteFile(filepath.Join(root, "DEBIAN", "control"), []byte(control), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, out)
	if b, err := exec.Command("dpkg-deb", "--build", root, dst).CombinedOutput(); err != nil {
		t.Skipf("could not build a test package: %v: %s", err, b)
	}
}
