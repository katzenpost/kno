package activate

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

const copyInstallScript = `set -u
fail=0
check() { if eval "$2"; then echo "  ok    $1"; else echo "  FAIL  $1"; fail=$((fail+1)); fi; }
apt-get update -qq >/dev/null && apt-get install -y -qq sudo >/dev/null 2>&1
install -m 0755 /d/kno /usr/local/bin/kno
useradd -m -G sudo op
printf '%%sudo ALL=(ALL:ALL) NOPASSWD: ALL\n' >/etc/sudoers.d/test-admin
chmod 0440 /etc/sudoers.d/test-admin
mkdir -p /etc/katzenpost
h=$(hostname)
printf 'network = "namenlos"\nnodes = ["mixy"]\n\n[hosts."%s"]\nhostname = "%s"\n' "$h" "$h" >/etc/katzenpost/kno.toml
mkdir -p /etc/cron.d
dir=/home/op/.local/share/katzenpost/kno/pkgs/v9.9.9
su op -c "mkdir -p $dir/pkgs && cp /d/pkgs/* $dir/pkgs/"
check "the user stages the release without root" "[ -f $dir/pkgs/SHA256SUMS ]"
su op -c "sudo -n kno activate $dir v9.9.9" >/tmp/activate.log 2>&1
check "sudo kno activate installs the staged packages" "dpkg -s katzenpost >/dev/null 2>&1"
su op -c "sudo -n kno activate --schedule=cron" >/tmp/schedule.log 2>&1
check "sudo kno activate sets the scheduler" "[ -f /etc/cron.d/kno-update ]"
check "kno wrote no sudo config" "! [ -e /etc/sudoers.d/kno-activate ]"
[ "$fail" = 0 ] || cat /tmp/activate.log /tmp/schedule.log
echo "RESULT fail=$fail"
`

func TestACopyStagedReleaseInstallsThroughTheAdminsSudo(t *testing.T) {
	if os.Getenv("KNO_DEB_INSTALL_TEST") == "" {
		t.Skip("set KNO_DEB_INSTALL_TEST=1 to run the container install test")
	}
	podman, err := exec.LookPath("podman")
	if err != nil {
		t.Skip("podman not available")
	}
	dir := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(dir, "kno"), "./cmd/kno")
	build.Dir = testutil.ModuleRoot(t)
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build kno: %v\n%s", err, out)
	}
	pkgs := filepath.Join(dir, "pkgs")
	if err := os.MkdirAll(pkgs, 0o755); err != nil {
		t.Fatal(err)
	}
	var sums strings.Builder
	for _, n := range []string{"katzenpost_9.9.9+debian13_amd64.deb", "namenlos-config-mixy-pq-mixserver_9.9.9-1_amd64.deb"} {
		p := filepath.Join(pkgs, n)
		mkdeb(t, p, nil)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(b)
		sums.WriteString(hex.EncodeToString(h[:]) + "  " + n + "\n")
	}
	if err := os.WriteFile(filepath.Join(pkgs, "SHA256SUMS"), []byte(sums.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "check.sh"), []byte(copyInstallScript), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(podman, "run", "--rm", "-v", dir+":/d:ro,Z",
		"docker.io/library/debian:trixie@sha256:6788062a1b42ac281f053ac876170b79a3eaed5d61383b8ed7eaca6c6965f3b1",
		"sh", "/d/check.sh").CombinedOutput()
	t.Logf("container output:\n%s", out)
	if err != nil {
		t.Fatalf("podman run: %v", err)
	}
	if !strings.Contains(string(out), "RESULT fail=0") || strings.Count(string(out), "  ok    ") < 4 {
		t.Errorf("a copy-staged release did not install through the admin's sudo")
	}
}
