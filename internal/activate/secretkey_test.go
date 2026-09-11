package activate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheRealWorldSecretNames(t *testing.T) {
	for _, c := range []struct {
		name  string
		entry []string
		want  bool
	}{
		{"plain file", []string{"./var/lib/pq-katzenpost-mixserver/identity.private.pem"}, true},
		{"symlink named like a key", []string{"./var/lib/pq-katzenpost-mixserver/identity.private.pem -> /dev/null"}, true},
		{"genkeypair id.sign_private", []string{"./var/lib/pq-katzenpost-mixserver/id.sign_private.pem"}, true},
		{"genkeypair lk.kem_private", []string{"./var/lib/pq-katzenpost-mixserver/lk.kem_private.pem"}, true},
		{"public keys and config only", []string{
			"./etc/katzenpost/mixserver.toml",
			"./var/lib/pq-katzenpost-mixserver/identity.public.pem",
		}, false},
		{"empty package", nil, false},
	} {
		got := found(t, tarOf(t, c.entry...)) != ""
		if got != c.want {
			t.Errorf("%s: found=%v, want %v", c.name, got, c.want)
		}
	}
}

func TestScanSecretKeysRefusesUnreadableArchive(t *testing.T) {
	err := scanSecretKeys(context.Background(), Step{
		Argv: []string{"dpkg-deb", "--fsys-tarfile", "/nonexistent/not-a.deb"},
	})
	if err == nil {
		t.Fatal("want an error when the archive cannot be listed")
	}
	if !strings.Contains(err.Error(), "refusing to install") {
		t.Errorf("error should refuse, not just report: %v", err)
	}
}

func tarStreamFile(t *testing.T, names ...string) []string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "stream.tar")
	if err := os.WriteFile(p, tarOf(t, names...), 0o644); err != nil {
		t.Fatal(err)
	}
	return []string{"cat", p}
}

func TestScanSecretKeysPassesACleanPackage(t *testing.T) {
	argv := tarStreamFile(t,
		"./etc/katzenpost/mixserver.toml",
		"./var/lib/pq-katzenpost-mixserver/identity.public.pem",
	)
	if err := scanSecretKeys(context.Background(), Step{Argv: argv}); err != nil {
		t.Errorf("clean package refused: %v", err)
	}
}

func TestScanSecretKeysRefusesASymlinkedKey(t *testing.T) {
	err := scanSecretKeys(context.Background(), Step{
		Argv: tarStreamFile(t, "./var/lib/pq-katzenpost-mixserver/identity.private.pem -> /dev/null"),
	})
	if err == nil {
		t.Fatal("a symlinked private key must be refused")
	}
	if !strings.Contains(err.Error(), "private key") {
		t.Errorf("error should name the reason: %v", err)
	}
}

func TestApplyRefusesARealDebCarryingAKey(t *testing.T) {
	svcs := []string{"mixy-pq-mixserver"}
	rel := stageRelease(t, svcs...)
	mkpkgs(t, rel, "katzenpost_0.0.104_amd64.deb")
	mkdeb(t, filepath.Join(rel, "pkgs", "namenlos-config-mixy-pq-mixserver_0.0.104_all.deb"),
		[]string{"var/lib/pq-katzenpost-mixserver/identity.private.pem"})

	o := Options{Rel: rel, Tag: testTag, Services: svcs, Arch: "amd64", DryRun: true}
	p, err := PlanFor(o)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	err = Apply(context.Background(), p, o, &b)
	if err == nil {
		t.Fatalf("a package shipping a private key was accepted:\n%s", b.String())
	}
	if !strings.Contains(err.Error(), "private key") {
		t.Errorf("wrong refusal: %v", err)
	}

	if strings.Contains(b.String(), "dpkg -i") {
		t.Errorf("the install was still planned to run:\n%s", b.String())
	}
}

func TestApplyAcceptsTheSamePackageWithoutTheKey(t *testing.T) {
	svcs := []string{"mixy-pq-mixserver"}
	rel := stageRelease(t, svcs...)
	mkpkgs(t, rel, "katzenpost_0.0.104_amd64.deb")
	mkdeb(t, filepath.Join(rel, "pkgs", "namenlos-config-mixy-pq-mixserver_0.0.104_all.deb"),
		[]string{"var/lib/pq-katzenpost-mixserver/identity.public.pem"})

	o := Options{Rel: rel, Tag: testTag, Services: svcs, Arch: "amd64", DryRun: true}
	p, err := PlanFor(o)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := Apply(context.Background(), p, o, &b); err != nil {
		t.Fatalf("clean package refused: %v\n%s", err, b.String())
	}
}
