package sim

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/testutil"
)

func TestEveryAuthorityValidatesWithItsOwnPublicIdentity(t *testing.T) {
	repo := testutil.RepoRoot(t)
	keys := t.TempDir()
	if _, err := KeyTree(repo, "namenlos", keys); err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(knopath.EditableDir(repo, "namenlos"), "nodes", "*-authority.toml"))
	if err != nil || len(paths) != 6 {
		t.Fatalf("authority nodes: %d %v", len(paths), err)
	}
	seen := map[string]bool{}
	for _, path := range paths {
		var node struct{ ConfigName, IdentityPublicKey string }
		if _, err := toml.DecodeFile(path, &node); err != nil {
			t.Fatal(err)
		}
		o := Options{Repo: repo, Network: "namenlos", keys: keys}
		dir, cleanup, err := o.validationKeys(ConfigPath(repo, "namenlos", node.ConfigName))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(dir, "identity.public.pem"))
		if err != nil || strings.TrimSpace(string(b)) != strings.TrimSpace(node.IdentityPublicKey) {
			t.Fatalf("%s got the wrong public identity: %v", node.ConfigName, err)
		}
		if seen[string(b)] {
			t.Fatal("two authorities received the same self identity")
		}
		seen[string(b)] = true
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.Contains(entry.Name(), "private") || strings.Contains(entry.Name(), "secret") {
				t.Fatalf("validation requires no secrets: %s", entry.Name())
			}
		}
		if _, err := os.Stat(filepath.Join(dir, "wauland2_mix_id_pub.key.pem")); err != nil {
			t.Fatal("authority peer keys were omitted", err)
		}
		cleanup()
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("temporary authority keys were not cleaned up")
		}
	}
	if _, err := os.Stat(filepath.Join(keys, "identity.public.pem")); !os.IsNotExist(err) {
		t.Fatal("authority identity leaked into the shared peer tree")
	}
}

func TestAuthorityValidationDoesNotInventAMissingSelfIdentity(t *testing.T) {
	repo := t.TempDir()
	cfg := filepath.Join(repo, "absent-pq-authority.toml")
	if err := os.WriteFile(cfg, []byte("[Server]\nIdentifier = \"absent\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := Options{Repo: repo, Network: "n", keys: t.TempDir()}
	_, cleanup, err := o.validationKeys(cfg)
	defer cleanup()
	if err == nil {
		t.Fatal("missing independent authority identity was accepted")
	}
}
