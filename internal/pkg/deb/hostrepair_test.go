package deb

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/release"
	"github.com/katzenpost/kno/internal/testutil"
)

const brokenNode = `#!/bin/sh
set -eu
mkdir -p /var/lib/pq-katzenpost-mixserver /usr/local/bin
# Stand-ins for the node's irreplaceable identity keys, in the layout of
# the hand-built packages. If the install ever removes or truncates one
# of these, this test is why we found out here.
printf 'IDENTITY-KEY\n' > /var/lib/pq-katzenpost-mixserver/identity.private.pem
printf 'LINK-KEY\n'     > /var/lib/pq-katzenpost-mixserver/link.private.pem
printf '#!/bin/sh\nexit 0\n' > /usr/local/bin/pq-katzenpost-mixserver
chmod 0755 /usr/local/bin/pq-katzenpost-mixserver
# The katzenpost package creates this account; the config package needs it.
useradd --system --shell /usr/sbin/nologin katzenpost
echo "fixture: keys in the old layout, katzenpost account present"
`

const checkNode = `#!/bin/sh
set -u
fail=0
ck() { if eval "$2"; then echo "  ok    $1"; else echo "  FAIL  $1"; fail=1; fi; }
old=/var/lib/pq-katzenpost-mixserver
new=/var/lib/katzenpost/server/oneeyedman-pq-mixserver
cfg=/etc/katzenpost/server/oneeyedman-pq-mixserver.toml

sh /d/broken.sh || exit 1
dpkg -i --force-depends /d/config.deb || fail=1

ck "state dir owned"            "[ \"$(stat -c %U $new)\" = katzenpost ]"
ck "state dir 0700"             "[ \"$(stat -c %a $new)\" = 700 ]"
ck "config readable by group"   "[ \"$(stat -c %a $cfg)\" = 640 ]"
ck "config group is the account" "[ \"$(stat -c %G $cfg)\" = katzenpost ]"
ck "move recorded"              "grep -qx $old $new/.kno-migrated-from"

echo "== the keys that were already there =="
ck "identity key moved"         "[ -f $new/identity.private.pem ]"
ck "identity key intact"        "grep -qx IDENTITY-KEY $new/identity.private.pem"
ck "link key intact"            "grep -qx LINK-KEY $new/link.private.pem"
ck "keys owned by the account"  "[ \"$(stat -c %U $new/identity.private.pem)\" = katzenpost ]"
ck "no key left behind"         "! [ -e $old/identity.private.pem ] && ! [ -e $old/link.private.pem ]"

echo "RESULT fail=$fail"
`

func TestConfigPackageMovesTheKeysOfAnOldLayoutHost(t *testing.T) {
	if os.Getenv("KNO_DEB_INSTALL_TEST") == "" {
		t.Skip("set KNO_DEB_INSTALL_TEST=1 to run the container install test")
	}
	podman, err := exec.LookPath("podman")
	if err != nil {
		t.Skip("podman not available")
	}
	repo := testutil.RepoRoot(t)
	tmp := t.TempDir()

	svc := "oneeyedman-pq-mixserver"
	rel := filepath.Join(tmp, "rel")
	if err := release.Stage(repo, "namenlos", rel, []string{svc}, nil); err != nil {
		t.Fatalf("stage: %v", err)
	}
	root, err := AssembleConfigRoot(rel, "v0.0.104", "1", svc, filepath.Join(tmp, "roots"), "amd64")
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	out := filepath.Join(tmp, "config.deb")
	if err := Build(context.Background(), root, out); err != nil {
		t.Fatalf("build: %v", err)
	}

	dir := t.TempDir()
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.deb"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, f := range [][2]string{{"broken.sh", brokenNode}, {"check.sh", checkNode}} {
		if err := os.WriteFile(filepath.Join(dir, f[0]), []byte(f[1]), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	res, err := exec.Command(podman, "run", "--rm", "--network=none",
		"-v", dir+":/d:ro,Z", "debian:bookworm-slim", "sh", "/d/check.sh").CombinedOutput()
	t.Logf("container output:\n%s", res)
	if err != nil {
		t.Fatalf("podman run: %v", err)
	}
	if !strings.Contains(string(res), "RESULT fail=0") {
		t.Errorf("installing the config package does not move the keys of an old layout host intact")
	}
	if n := strings.Count(string(res), "  ok    "); n < 10 {
		t.Errorf("only %d assertions ran; the check script did not complete", n)
	}
}
