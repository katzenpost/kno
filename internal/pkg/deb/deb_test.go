package deb

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/release"
	"github.com/katzenpost/kno/internal/testutil"
)

func TestConfigRootRefusesASecretKey(t *testing.T) {
	repo := testutil.RepoRoot(t)
	tmp := t.TempDir()
	tag := "v0.0.104"
	svc := "luscus-pq-authority"

	rel := filepath.Join(tmp, "rel", tag)
	if err := release.Stage(repo, "namenlos", rel, []string{svc}, nil); err != nil {
		t.Fatalf("Stage: %v", err)
	}

	if _, err := AssembleConfigRoot(rel, tag, "", svc, filepath.Join(tmp, "clean"), "amd64"); err != nil {
		t.Fatalf("AssembleConfigRoot: %v", err)
	}

	keys, err := os.ReadDir(filepath.Join(rel, "keys"))
	if err != nil || len(keys) == 0 {
		t.Fatalf("no staged key dirs to poison: %v", err)
	}
	planted := filepath.Join(rel, "keys", keys[0].Name(), "identity.private.pem")
	if err := os.WriteFile(planted, []byte("-----BEGIN PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = AssembleConfigRoot(rel, tag, "", svc, filepath.Join(tmp, "poisoned"), "amd64")
	if err == nil {
		t.Fatal("a staged secret key must stop the package build")
	}
	if !strings.Contains(err.Error(), "private key") && !strings.Contains(err.Error(), "secret") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

func TestConfigPackageDoesNotOwnTheDaemonsOwnKeyFiles(t *testing.T) {
	repo := testutil.RepoRoot(t)
	tmp := t.TempDir()
	const tag, svc = "v0.0.104", "oneeyedman-pq-mixserver"

	rel := filepath.Join(tmp, "rel", tag)
	if err := release.Stage(repo, "namenlos", rel, []string{svc}, nil); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	root, err := AssembleConfigRoot(rel, tag, "", svc, filepath.Join(tmp, "roots"), "amd64")
	if err != nil {
		t.Fatalf("AssembleConfigRoot: %v", err)
	}

	var shipped []string
	if err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rp, _ := filepath.Rel(root, p)
		shipped = append(shipped, "/"+rp)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(shipped)

	for _, owned := range []string{
		"identity.public.pem", "identity.private.pem",
		"link.public.pem", "link.private.pem",
	} {
		for _, f := range shipped {
			if filepath.Base(f) == owned {
				t.Errorf("the config package now ships %s; doctor tells operators"+
					" that no package owns it, and that advice must change:\n  %s", owned, f)
			}
		}
	}

	want := "/var/lib/katzenpost/server/oneeyedman-pq-mixserver/oneeyedman_mix_id_pub_key.pem"
	found := false
	for _, f := range shipped {
		if f == want {
			found = true
		}
	}
	if !found {
		t.Errorf("the node's own public key is not shipped to %s; doctor names it"+
			" as the replacement for a wrong identity.public.pem.\nshipped:\n  %s",
			want, strings.Join(shipped, "\n  "))
	}
}

func TestConfigRootNeedsItsManifestFields(t *testing.T) {
	tmp := t.TempDir()
	rel := filepath.Join(tmp, "rel")
	if err := os.MkdirAll(filepath.Join(rel, "manifest"), 0o755); err != nil {
		t.Fatal(err)
	}
	frag := filepath.Join(rel, "manifest", "broken-pq-mixserver.conf")
	if err := os.WriteFile(frag, []byte("CONFIG_DST=\"/etc/x.toml\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := AssembleConfigRoot(rel, "v0.0.104", "", "broken-pq-mixserver", filepath.Join(tmp, "root"), "amd64")
	if err == nil {
		t.Fatal("an incomplete manifest fragment must not produce a package")
	}
}

func TestTheShippedUnitCanBindAPrivilegedPort(t *testing.T) {
	tmp := t.TempDir()
	repo := testutil.RepoRoot(t)
	const svc = "reed-pq-mixserver"
	rel := filepath.Join(tmp, "rel")
	if err := release.Stage(repo, "namenlos", rel, []string{svc}, nil); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	root, err := AssembleConfigRoot(rel, "v0.0.104", "", svc,
		filepath.Join(tmp, "roots"), "amd64")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "usr/lib/systemd/system/kpserver@reed-pq-mixserver.service.d/kno.conf"))
	if err != nil {
		t.Fatal(err)
	}
	unit := string(b)
	for _, want := range []string{
		"AmbientCapabilities=CAP_NET_BIND_SERVICE",

		"CapabilityBoundingSet=CAP_NET_BIND_SERVICE",

		"LogsDirectory=katzenpost",

		"RequiresMountsFor=/var/lib/katzenpost/server/reed-pq-mixserver",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("the shipped unit has no %q:\n%s", want, unit)
		}
	}
}
