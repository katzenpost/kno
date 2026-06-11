package release

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/peers"
	"github.com/katzenpost/kno/internal/testutil"
)

func fileList(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

var stageServices = []string{
	"luscus-pq-authority",
	"oneeyedman-pq-mixserver",
	"annares-pq-servicenode",
	"annares-pq-storagenode-0",
}

func stage(t *testing.T, peers map[string]string) string {
	t.Helper()
	rel := filepath.Join(t.TempDir(), "v0.0.104")
	if err := Stage(testutil.RepoRoot(t), "namenlos", rel, stageServices, peers); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	return rel
}

func TestStageProducesTheReleaseLayout(t *testing.T) {
	rel := stage(t, nil)

	for _, svc := range stageServices {
		if _, err := os.Stat(filepath.Join(rel, "manifest", svc+".conf")); err != nil {
			t.Errorf("no manifest fragment for %s: %v", svc, err)
		}
		if _, err := os.Stat(filepath.Join(rel, "configs", svc+".toml")); err != nil {
			t.Errorf("no staged config for %s: %v", svc, err)
		}
	}

	if _, err := os.Stat(filepath.Join(rel, "configs", "courier.toml")); err != nil {
		t.Errorf("courier aux config was not staged: %v", err)
	}
	frag := readFile(t, filepath.Join(rel, "manifest", "annares-pq-servicenode.conf"))
	if !strings.Contains(frag, "courier.toml=") {
		t.Errorf("manifest fragment does not carry AUX_FILES:\n%s", frag)
	}
	for _, f := range []string{"client.toml", "thinclient.toml"} {
		if _, err := os.Stat(filepath.Join(rel, f)); err != nil {
			t.Errorf("%s was not staged: %v", f, err)
		}
	}
	if n := len(fileList(t, filepath.Join(rel, "keys"))); n == 0 {
		t.Error("no public keys staged")
	}
}

func TestStageNeverStagesASecret(t *testing.T) {
	rel := stage(t, nil)
	if s, err := fsutil.FindSecret(rel); err != nil || s != "" {
		t.Errorf("a secret key reached the staged release: %q (%v)", s, err)
	}

	poisoned := filepath.Join(t.TempDir(), "keys")
	if err := os.MkdirAll(poisoned, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(poisoned, "identity.private.pem"),
		[]byte("-----BEGIN PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := fsutil.CopyTreeGuarded(poisoned, filepath.Join(t.TempDir(), "dst"))
	if err == nil {
		t.Fatal("copying a key source that holds a secret must fail")
	}
	if !strings.Contains(err.Error(), "secret") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

func TestStageRewritesPeerAddresses(t *testing.T) {
	const mapped = "tcp://10.99.0.7:30001"
	plain := stage(t, nil)
	rel := stage(t, map[string]string{"gnunet": mapped})

	before := readFile(t, filepath.Join(plain, "configs", "oneeyedman-pq-mixserver.toml"))
	lan := peers.NodePeers(testutil.RepoRoot(t), "namenlos", "oneeyedman")["luscus"]
	if lan == "" {
		t.Fatal("the fixture no longer has a same-LAN peer; this test covers nothing")
	}
	if !strings.Contains(before, `Addresses = ["`+lan+`"]`) {
		t.Errorf("the same-LAN peer was not rewritten to %s", lan)
	}

	cfg := readFile(t, filepath.Join(rel, "configs", "oneeyedman-pq-mixserver.toml"))
	if !strings.Contains(cfg, `Addresses = ["`+mapped+`"]`) {
		t.Errorf("the operator's peer override was not applied (%s)", mapped)
	}
	if before == cfg {
		t.Fatal("the override changed nothing; the fixture no longer covers it")
	}

	own := func(s string) string { return strings.SplitN(s, "[[", 2)[0] }
	if own(before) != own(cfg) {
		t.Errorf("the node's own block was rewritten:\n%s\n%s", own(before), own(cfg))
	}
}

func TestStageIsAtomic(t *testing.T) {
	rel := stage(t, nil)
	if _, err := os.Stat(rel + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("%s.tmp survived the stage", rel)
	}
}
