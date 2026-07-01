package freebsd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/knopath"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/testutil"
)

var upstreamCmds = []string{
	"courier", "dirauth", "echo-plugin", "fetch", "genconfig", "genkeypair",
	"geometry", "http-proxy-client", "http-proxy-server", "kpclientd", "kpconfig",
	"motd", "ping", "replica", "server", "sphinx",
}

func fakeBins(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, b := range upstreamCmds {
		if err := os.WriteFile(filepath.Join(dir, b), []byte("BIN:"+b+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStageLaysOutThePackageRoot(t *testing.T) {
	repo := testutil.RepoRoot(t)
	tmp := t.TempDir()
	tag := "v0.0.104"
	t.Setenv("SOURCE_DATE_EPOCH", "1600000000")

	bindir := filepath.Join(tmp, "bin")
	fakeBins(t, bindir)
	rel := filepath.Join(tmp, "rel", tag)
	if err := os.MkdirAll(rel, 0o755); err != nil {
		t.Fatal(err)
	}
	client := filepath.Join(knopath.ConfigsDir(repo, "namenlos"), "client.toml")
	if err := fsutil.CopyFile(client, filepath.Join(rel, "client.toml")); err != nil {
		t.Fatal(err)
	}

	root, err := Stage(bindir, rel, repo)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	got := testutil.TreeSnapshot(t, root)
	if len(got) == 0 {
		t.Fatal("Stage produced an empty root")
	}

	for _, want := range []string{
		"usr/local/sbin/kpdirauth",
		"usr/local/sbin/kpserver",
		"usr/local/sbin/kpreplica",
		"usr/local/sbin/kpcourier",
		"usr/local/bin/kpgenkeypair",
		"usr/local/bin/kpping",
		"usr/local/bin/kpclientd",
		"usr/local/bin/kpconfig",
		"usr/local/bin/kpfetch",
		"usr/local/bin/kpgenconfig",
		"usr/local/bin/kpecho-plugin",
		"usr/local/bin/kpgeometry",
		"usr/local/bin/kphttp-proxy-client",
		"usr/local/bin/kphttp-proxy-server",
		"usr/local/bin/kpmotd",
		"usr/local/bin/kpsphinx",
		"usr/local/etc/kpclientd/client.toml",
		"usr/local/etc/rc.d/katzenpost",
	} {
		if _, ok := got[want]; !ok {
			var have []string
			for k := range got {
				have = append(have, k)
			}
			sort.Strings(have)
			t.Errorf("missing %s from the staged root; have: %v", want, have)
		}
	}

	for k := range got {
		if fsutil.IsSecretKey(filepath.Base(k)) {
			t.Errorf("the staged root contains a private key: %s", k)
		}
	}
}

func TestPackageIsReproducible(t *testing.T) {
	repo := testutil.RepoRoot(t)
	tmp := t.TempDir()
	tag := "v0.0.104"
	t.Setenv("SOURCE_DATE_EPOCH", "1600000000")

	bindir := filepath.Join(tmp, "bin")
	fakeBins(t, bindir)
	rel := filepath.Join(tmp, "rel", tag)
	if err := os.MkdirAll(rel, 0o755); err != nil {
		t.Fatal(err)
	}
	client := filepath.Join(knopath.ConfigsDir(repo, "namenlos"), "client.toml")
	if err := fsutil.CopyFile(client, filepath.Join(rel, "client.toml")); err != nil {
		t.Fatal(err)
	}

	out, err := Package(bindir, rel, tag, "", repo, DefaultABI)
	if err != nil {
		t.Fatalf("Package: %v", err)
	}
	if filepath.Base(out) != pkgName+"-0.0.104.pkg" {
		t.Errorf("pkg name = %s, want %s-0.0.104.pkg", filepath.Base(out), pkgName)
	}
	first, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Package(bindir, rel, tag, "", repo, DefaultABI); err != nil {
		t.Fatalf("Package (second run): %v", err)
	}
	second, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("%s is not reproducible: %d bytes then %d bytes",
			filepath.Base(out), len(first), len(second))
	}

	if _, err := Validate(out); err != nil {
		t.Errorf("the package we just built does not validate: %v", err)
	}
}

func TestSysrootRejectsBadSha256(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("KNO_FREEBSD_CACHE", cache)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("corrupted, not a FreeBSD base.txz"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "sysroot")
	pins := map[string]string{
		"FREEBSD_BASE_URL":    srv.URL + "/base.txz",
		"FREEBSD_BASE_SHA256": strings.Repeat("a", 64),
	}
	_, err := Sysroot(dest, pins)
	if err == nil {
		t.Fatal("Sysroot accepted a corrupted download")
	}
	if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("wrong failure: %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("sysroot dir exists after a failed checksum: %s", dest)
	}
	ents, err := os.ReadDir(cache)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 0 {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Errorf("corrupt download left in the cache: %v", names)
	}
}

func TestSysrootExtractsSelectively(t *testing.T) {
	for _, tool := range []string{"tar", "xz"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available", tool)
		}
	}
	src := t.TempDir()
	for _, f := range []string{"lib/libc.so.7", "usr/lib/libssl.so", "usr/include/stdio.h", "boot/kernel"} {
		p := filepath.Join(src, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tarball := filepath.Join(t.TempDir(), "base.txz")
	mk := exec.Command("tar", "-cJf", tarball, "-C", src, "./lib", "./usr", "./boot")
	if out, err := mk.CombinedOutput(); err != nil {
		t.Fatalf("tar -cJf: %v\n%s", err, out)
	}
	body, err := os.ReadFile(tarball)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "mirror down", http.StatusInternalServerError)
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer fallback.Close()

	cache := t.TempDir()
	t.Setenv("KNO_FREEBSD_CACHE", cache)
	dest := filepath.Join(t.TempDir(), "sysroot")
	got, err := Sysroot(dest, map[string]string{
		"FREEBSD_BASE_URL":          primary.URL + "/base.txz",
		"FREEBSD_BASE_URL_FALLBACK": fallback.URL + "/base.txz",
		"FREEBSD_BASE_SHA256":       hex.EncodeToString(sum[:]),
	})
	if err != nil {
		t.Fatalf("Sysroot: %v", err)
	}
	if got != dest {
		t.Errorf("Sysroot returned %s, want %s", got, dest)
	}
	for _, f := range []string{"lib/libc.so.7", "usr/lib/libssl.so", "usr/include/stdio.h"} {
		if _, err := os.Stat(filepath.Join(dest, f)); err != nil {
			t.Errorf("missing from the sysroot: %s (%v)", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "boot")); !os.IsNotExist(err) {
		t.Errorf("extracted ./boot; only %v belong in the sysroot", sysrootDirs)
	}
	if _, err := os.Stat(filepath.Join(cache, "base.txz")); err != nil {
		t.Errorf("verified download was not cached: %v", err)
	}
}
