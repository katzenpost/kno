package doctor

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/hostsetup"
)

const adviceScript = `#!/bin/sh
set -u
fail=0
ck() { if eval "$2"; then echo "  ok    $1"; else echo "  FAIL  $1"; fail=1; fi; }
useradd --system --home-dir /var/lib/kno-updater --create-home --shell /usr/sbin/nologin kno

ck "the image's own account holds the fixed range" "grep -q ':100000:' /etc/subuid"
usermod --add-subuids "$RANGE" --add-subgids "$RANGE" kno
ck "kno got the computed range" "grep -q '^kno:' /etc/subuid"
start=$(awk -F: '$1=="kno" {print $2}' /etc/subuid)
ck "it is not the fixed 100000" "test \"$start\" != 100000"
dupes=$(cut -d: -f2 /etc/subuid | sort | uniq -d | wc -l)
ck "no two accounts share a start" "test $dupes -eq 0"
echo "RESULT fail=$fail"
`

func TestDoctorsSubidAdviceDoesNotCollide(t *testing.T) {
	podman, err := exec.LookPath("podman")
	if err != nil {
		t.Skip("podman not available")
	}
	dir := t.TempDir()
	script := dir + "/advice.sh"
	if err := os.WriteFile(script, []byte(adviceScript), 0o755); err != nil {
		t.Fatal(err)
	}

	const image = "ubuntu:24.04"
	have, err := exec.Command(podman, "run", "--rm", image,
		"sh", "-c", "cat /etc/subuid 2>/dev/null || true").Output()
	if err != nil {
		t.Skipf("could not read the image's /etc/subuid: %v", err)
	}
	_, next := hostsetup.ScanSubids(string(have), config.UpdaterAccount)
	rng := hostsetup.FormatRange(next)
	if rng == "" {
		t.Skip("no range computed")
	}

	out, err := exec.Command(podman, "run", "--rm", "-e", "RANGE="+rng,
		"-v", script+":/a.sh:ro,Z", image, "sh", "/a.sh").CombinedOutput()
	t.Logf("advice range %s on %s:\n%s", rng, image, out)
	if err != nil {
		t.Fatalf("podman run: %v", err)
	}
	if !strings.Contains(string(out), "RESULT fail=0") {
		t.Error("the range doctor would print collides on this image")
	}
}
