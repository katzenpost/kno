package hostsetup

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

const subidScript = `#!/bin/sh
set -u
fail=0
ck() { if eval "$2"; then echo "  ok    $1"; else echo "  FAIL  $1"; fail=1; fi; }

useradd --system --home-dir /var/lib/kno-updater --create-home --shell /usr/sbin/nologin kno
echo "== a --system account gets no subordinate range =="
ck "kno has no subuid" "! grep -q '^kno:' /etc/subuid"
ck "kno has no subgid" "! grep -q '^kno:' /etc/subgid"

echo "== and the range kno grants does not collide =="
# The image's own default account holds 100000-165535 on Ubuntu, which is
# exactly what a hardcoded range would have handed kno as well.
usermod --add-subuids "$RANGE" --add-subgids "$RANGE" kno
ck "kno now has a subuid range" "grep -q '^kno:' /etc/subuid"
ck "kno now has a subgid range" "grep -q '^kno:' /etc/subgid"
ck "no two accounts share a start" "test $(cut -d: -f2 /etc/subuid | sort | uniq -d | wc -l) -eq 0"
overlap=$(awk -F: '{print $2, $2+$3-1}' /etc/subuid | sort -n | awk 'NR>1 && $1<=prev { print "overlap" } { prev=$2 }')
ck "no range overlaps another" "test -z '$overlap'"

echo "== granting it twice changes nothing =="
before=$(cat /etc/subuid)
usermod --add-subuids "$RANGE" --add-subgids "$RANGE" kno
ck "idempotent" "test \"$before\" = \"$(cat /etc/subuid)\""

echo "RESULT fail=$fail"
`

func TestSystemAccountNeedsASubidRange(t *testing.T) {
	podman, err := exec.LookPath("podman")
	if err != nil {
		t.Skip("podman not available")
	}
	dir := t.TempDir()
	script := dir + "/subid.sh"
	if err := os.WriteFile(script, []byte(subidScript), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, image := range []string{"ubuntu:24.04", "debian:bookworm-slim"} {
		t.Run(image, func(t *testing.T) {

			have, err := exec.Command(podman, "run", "--rm", "-v", script+":/s.sh:ro,Z",
				image, "sh", "-c", "cat /etc/subuid 2>/dev/null || true").Output()
			if err != nil {
				t.Skipf("could not read the image's /etc/subuid: %v", err)
			}
			_, next := ScanSubids(string(have), "kno")
			rng := FormatRange(next)

			out, err := exec.Command(podman, "run", "--rm",
				"-e", "RANGE="+rng,
				"-v", script+":/s.sh:ro,Z", image, "sh", "/s.sh").CombinedOutput()
			t.Logf("%s:\n%s", image, out)
			if err != nil {
				t.Fatalf("podman run: %v", err)
			}
			if !strings.Contains(string(out), "RESULT fail=0") {
				t.Errorf("the subordinate id assumptions do not hold on %s", image)
			}
			if n := strings.Count(string(out), "  ok    "); n < 7 {
				t.Errorf("only %d assertions ran on %s", n, image)
			}
		})
	}
}
