package deb

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/release"
	"github.com/katzenpost/kno/internal/testutil"
)

type fixture struct {
	t       *testing.T
	root    string
	scripts map[string]string
	path    string
}

func newFixture(t *testing.T, svcs ...string) *fixture {
	t.Helper()
	for _, tool := range []string{"sh", "dpkg"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available", tool)
		}
	}
	repo := testutil.RepoRoot(t)
	tmp := t.TempDir()
	rel := filepath.Join(tmp, "rel", "v0.0.107")
	if err := release.Stage(repo, "namenlos", rel, svcs, nil); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	f := &fixture{t: t, root: filepath.Join(tmp, "root"), scripts: map[string]string{}}
	for _, svc := range svcs {
		pkgRoot, err := AssembleConfigRoot(rel, "v0.0.107", "1", svc, filepath.Join(tmp, "pkgs"), "amd64")
		if err != nil {
			t.Fatalf("AssembleConfigRoot %s: %v", svc, err)
		}
		for _, s := range []string{"preinst", "postinst", "prerm"} {
			f.scripts[svc+"/"+s] = filepath.Join(pkgRoot, "DEBIAN", s)
		}
		if err := filepath.Walk(pkgRoot, func(p string, fi os.FileInfo, err error) error {
			if err != nil || fi.IsDir() || strings.Contains(p, "/DEBIAN/") {
				return err
			}
			rp, _ := filepath.Rel(pkgRoot, p)
			f.write(rp, readOrDie(t, p))
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, fake := range []string{"chown", "systemctl"} {
		if err := os.WriteFile(filepath.Join(bin, fake), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.path = bin + ":" + os.Getenv("PATH")
	return f
}

func readOrDie(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (f *fixture) write(rel, body string) {
	f.t.Helper()
	p := filepath.Join(f.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) read(rel string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(f.root, rel))
	return string(b), err == nil
}

func (f *fixture) run(svc, script string, args ...string) (string, error) {
	f.t.Helper()
	c := exec.Command("sh", append([]string{f.scripts[svc+"/"+script]}, args...)...)
	c.Env = append(os.Environ(), "DPKG_ROOT="+f.root, "PATH="+f.path)
	b, err := c.CombinedOutput()
	return string(b), err
}

func (f *fixture) must(svc, script string, args ...string) {
	f.t.Helper()
	if out, err := f.run(svc, script, args...); err != nil {
		f.t.Fatalf("%s %s %v: %v\n%s", svc, script, args, err, out)
	}
}

func (f *fixture) has(rel, want string) {
	f.t.Helper()
	got, ok := f.read(rel)
	if !ok {
		f.t.Errorf("/%s is missing", rel)
	} else if want != "" && got != want {
		f.t.Errorf("/%s holds %q, want %q", rel, got, want)
	}
}

func (f *fixture) lacks(rel string) {
	f.t.Helper()
	if _, err := os.Lstat(filepath.Join(f.root, rel)); err == nil {
		f.t.Errorf("/%s is present", rel)
	}
}

const (
	srv  = "annares-pq-servicenode"
	rep0 = "annares-pq-storagenode-0"
	rep1 = "annares-pq-storagenode-1"
	oldS = "var/lib/pq-katzenpost-mixserver"
	newS = "var/lib/katzenpost/server/" + srv
	newR = "var/lib/katzenpost/replica/" + rep0
)

func annaresFixture(t *testing.T) *fixture {
	f := newFixture(t, srv, rep0, rep1)
	f.write(oldS+"/identity.private.pem", "server identity")
	f.write(oldS+"/link.private.pem", "server link")
	f.write(oldS+"/spool.db", "spool")
	f.write(oldS+"/annares_provider_id_pub_key.pem", "stale copy from the old package")
	f.write(oldS+"/storage_replica_0/identity.private.pem", "replica identity")
	f.write(oldS+"/storage_replica_0/replica.db", "user data")
	f.write(oldS+"/courier/link.private.pem", "courier link")
	return f
}

func TestTheUpgradeMovesEachServicesStateIntoItsOwnDir(t *testing.T) {
	f := annaresFixture(t)
	shipped, _ := f.read(newS + "/annares_provider_id_pub_key.pem")

	f.must(srv, "preinst", "upgrade", "0.0.104-259")
	f.must(srv, "postinst", "configure", "0.0.104-259")
	f.has(newS+"/identity.private.pem", "server identity")
	f.has(newS+"/link.private.pem", "server link")
	f.has(newS+"/spool.db", "spool")
	f.has(newS+"/annares_provider_id_pub_key.pem", shipped)
	f.lacks(newS + "/storage_replica_0")
	f.lacks(newS + "/courier")
	f.has("var/lib/katzenpost/courier/link.private.pem", "courier link")
	f.has(oldS+"/storage_replica_0/replica.db", "user data")

	f.must(rep0, "postinst", "configure", "0.0.104-259")
	f.has(newR+"/identity.private.pem", "replica identity")
	f.has(newR+"/replica.db", "user data")
	f.lacks(oldS + "/storage_replica_0/replica.db")

	f.must(srv, "postinst", "configure", "0.0.104-259")
	f.must(rep0, "postinst", "configure", "0.0.104-259")
	f.has(newS+"/identity.private.pem", "server identity")
	f.has(newR+"/replica.db", "user data")
}

func TestARollbackPutsTheStateBack(t *testing.T) {
	f := annaresFixture(t)
	f.must(srv, "postinst", "configure", "0.0.104-259")
	f.must(rep0, "postinst", "configure", "0.0.104-259")

	f.must(rep0, "prerm", "upgrade", "0.0.104-259")
	f.must(srv, "prerm", "upgrade", "0.0.104-259")
	f.has(oldS+"/identity.private.pem", "server identity")
	f.has(oldS+"/spool.db", "spool")
	f.has(oldS+"/courier/link.private.pem", "courier link")
	f.has(oldS+"/storage_replica_0/replica.db", "user data")
	f.lacks(newS + "/identity.private.pem")
	f.lacks(newR + "/replica.db")
	f.lacks("var/lib/katzenpost/courier")

	f.must(srv, "postinst", "configure", "0.0.104-259")
	f.has(newS+"/identity.private.pem", "server identity")
}

func TestAnUpgradeBetweenUpstreamReleasesMovesNothingBack(t *testing.T) {
	f := annaresFixture(t)
	f.must(srv, "postinst", "configure", "0.0.104-259")
	f.must(srv, "prerm", "upgrade", "0.0.108-1")
	f.has(newS+"/identity.private.pem", "server identity")
	f.lacks(oldS + "/identity.private.pem")
}

func TestANewIdentityStaysAndNothingMoves(t *testing.T) {
	f := annaresFixture(t)
	f.write(newS+"/identity.private.pem", "another identity")
	f.must(srv, "postinst", "configure", "0.0.104-259")
	f.has(oldS+"/identity.private.pem", "server identity")
	f.has(oldS+"/spool.db", "spool")
	f.has(newS+"/identity.private.pem", "another identity")
	f.lacks(newS + "/spool.db")
}

func TestStateIsNotMovedToAnotherFilesystem(t *testing.T) {
	f := annaresFixture(t)
	bin, _, _ := strings.Cut(f.path, ":")
	stat := "#!/bin/sh\ncase \"$3\" in *pq-katzenpost*) echo 1 ;; *) echo 2 ;; esac\n"
	if err := os.WriteFile(filepath.Join(bin, "stat"), []byte(stat), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := f.run(srv, "postinst", "configure", "0.0.104-259")
	if err == nil {
		t.Fatalf("postinst moved state to another filesystem:\n%s", out)
	}
	if !strings.Contains(out, "different filesystems") {
		t.Errorf("the refusal does not say why:\n%s", out)
	}
	f.has(oldS+"/identity.private.pem", "server identity")
	f.lacks(newS + "/identity.private.pem")
}

func TestThePreinstRenamesNoFile(t *testing.T) {
	f := newFixture(t, srv)
	b := readOrDie(t, f.scripts[srv+"/preinst"])
	if !strings.Contains(b, "dpkg-divert --remove --no-rename") || strings.Contains(b, "--remove --rename") {
		t.Errorf("the preinst can overwrite a file when it removes a diversion:\n%s", b)
	}
}

func TestAnOldDirThatHoldsTheNewRootIsNotMovedIntoItself(t *testing.T) {
	const svc = "cryptonymity-pq-mixserver"
	f := newFixture(t, svc)
	f.write("var/lib/katzenpost/identity.private.pem", "identity")
	f.write("var/lib/katzenpost/replay.db", "replay")
	f.must(svc, "postinst", "configure", "0.0.104-259")
	f.has("var/lib/katzenpost/server/"+svc+"/identity.private.pem", "identity")
	f.has("var/lib/katzenpost/server/"+svc+"/replay.db", "replay")
	f.lacks("var/lib/katzenpost/server/" + svc + "/server")
	f.lacks("var/lib/katzenpost/identity.private.pem")
}

func TestACourierMovesWhenTheServiceHasNoOldDir(t *testing.T) {
	const wsrv = "windfall-pq-servicenode"
	f := newFixture(t, wsrv)
	newW := "var/lib/katzenpost/server/" + wsrv
	if err := os.RemoveAll(filepath.Join(f.root, newW)); err != nil {
		t.Fatal(err)
	}
	f.lacks("var/lib/katzenpost-servicenode")
	f.write("var/lib/pq-katzenpost-mixserver/courier/link.private.pem", "courier link")

	f.must(wsrv, "postinst", "configure", "0.0.104-259")
	f.has("var/lib/katzenpost/courier/link.private.pem", "courier link")
	f.has(newW+"/.kno-migrated-moves", "")
}
