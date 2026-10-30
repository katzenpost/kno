package sim

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/katzenpost/kno/internal/activate"
	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/testutil"
)

func TestValidationSelectsTheCompleteNativeSimPackage(t *testing.T) {
	repo := testutil.RepoRoot(t)
	t.Setenv("KNO_ARTIFACTS", t.TempDir())
	rel := filepath.Join(os.Getenv("KNO_ARTIFACTS"), "releases", "v1", "pkgs")
	if err := os.MkdirAll(rel, 0o755); err != nil {
		t.Fatal(err)
	}
	p := newRun(NetOptions{Repo: repo, Network: "namenlos"}).clientPlatform()
	want := filepath.Join(rel, "katzenpost_1+sim"+compile.OSToken(p.OS)+"_"+activate.HostArch()+".deb")
	partial := filepath.Join(rel, "katzenpost_1+simubuntu25.04_"+activate.HostArch()+".deb")
	for _, path := range []string{want, partial} {
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := releaseDeb(repo, "namenlos", "v1"); err != nil || got != want {
		t.Fatalf("validation selected %s, want %s: %v", got, want, err)
	}
	if err := os.Remove(want); err != nil {
		t.Fatal(err)
	}
	if _, err := releaseDeb(repo, "namenlos", "v1"); err == nil {
		t.Fatal("partial runtime package was accepted without a native validation package")
	}
}

func TestValidationResolvesDockerDaemonAliases(t *testing.T) {
	dir := t.TempDir()
	for dst, binary := range map[string]string{
		"pq-katzenpost-authority":       "kpdirauth",
		"pq-katzenpost-storage-replica": "kpreplica",
	} {
		path := filepath.Join(dir, binary)
		if err := os.WriteFile(path, nil, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := daemonFor(dir, "", "/usr/local/bin/"+dst); got != path {
			t.Fatalf("%s resolved to %s, want %s", dst, got, path)
		}
	}
}
