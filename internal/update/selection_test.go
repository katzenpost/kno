package update

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/build"

	"github.com/katzenpost/kno/internal/testutil"
)

func TestRejectedTargetReportsInstalledAndBuiltPackagesWithoutRecording(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "checkout")
	cmd := exec.Command("git", "clone", "--shared", "-q", testutil.ModuleRoot(t), repo)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, b)
	}
	kp := filepath.Join(tmp, "katzenpost")
	initRepo(t, kp, "v0.0.107")
	cfg := filepath.Join(tmp, "kno.toml")
	write(t, cfg, "network = \"namenlos\"\nnodes = [\"firefly\"]\n[update]\npolicy = \"apply\"\nrequire_signed_tag = true\n[hosts.firefly]\nservices = [\"gnunet-pq-gatewaynode\"]\n")
	bin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	releases := filepath.Join(tmp, "releases")
	pkgs := filepath.Join(releases, "v0.0.107", "pkgs")
	if err := os.MkdirAll(pkgs, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "katzenpost_0.0.107+debian13_amd64.deb"
	body := []byte("fixture package")
	if err := os.WriteFile(filepath.Join(pkgs, name), body, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := fmt.Sprintf("%x", sha256.Sum256(body))
	m := build.PackageSums{Network: "namenlos", Tag: "v0.0.107", Packages: []build.PackageSum{{Name: name, Sha256: sum, Bytes: int64(len(body))}}}
	if err := build.WriteSums(pkgs, m); err != nil {
		t.Fatal(err)
	}
	if err := build.WriteSums(build.CommittedDir(filepath.Join(repo, "data"), "namenlos", m.Tag), m); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "data")
	git(t, repo, "commit", "-qm", "Record fixture package.")
	marker := filepath.Join(tmp, "sampled")
	if err := os.WriteFile(filepath.Join(bin, "dpkg-query"), []byte("#!/bin/sh\ntouch \"$SAMPLE_MARKER\"\nprintf 'katzenpost 0.0.107\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"KNO_REPO": repo, "KNO_HOME": filepath.Join(tmp, "home"),
		"KNO_CONFIG": cfg, "KNO_HOST": "firefly", "KATZENPOST_REPO": kp,
		"KNO_TRACK_FETCH": "0", "UPDATER_STATE_DIR": filepath.Join(tmp, "state"), "RELEASES_DIR": releases,
		"PATH": bin + string(os.PathListSeparator) + os.Getenv("PATH"), "SAMPLE_MARKER": marker,
	} {
		t.Setenv(k, v)
	}
	var out strings.Builder
	if rc := Start(&out, Flags{}); rc == 0 {
		t.Fatal("unsigned target was accepted")
	}
	if !strings.Contains(out.String(), "require_signed_tag") {
		t.Fatalf("signature refusal was lost: %s", out.String())
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("installed packages were not queried before target selection")
	}
	for _, want := range []string{"installed packages:", name, sum, "1 built package(s) match the committed hashes"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q: %s", want, out.String())
		}
	}
	if strings.Index(out.String(), "installed packages:") > strings.Index(out.String(), "require_signed_tag") {
		t.Fatalf("policy refusal preceded installed package report: %s", out.String())
	}
	if strings.Contains(out.String(), "seen with no deployment") {
		t.Fatalf("rejected target produced a deployment observation: %s", out.String())
	}
	cmd = exec.Command("git", "-C", repo, "status", "--porcelain")
	if b, err := cmd.Output(); err != nil || len(b) != 0 {
		t.Fatalf("rejected target changed the repository: %s (%v)", b, err)
	}
}
