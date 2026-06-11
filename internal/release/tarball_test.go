package release

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sha256File(t *testing.T, p string) string {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func tarballFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "server"), []byte("ELF-ish"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("read me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "etc", "mix.toml"), []byte("x = 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestTarballIsDeterministic(t *testing.T) {
	src := tarballFixture(t)
	out := t.TempDir()
	a := filepath.Join(out, "a.tar.gz")
	b := filepath.Join(out, "b.tar.gz")
	if err := Tarball(src, "kno-v1", a, 1700000000); err != nil {
		t.Fatal(err)
	}
	if err := Tarball(src, "kno-v1", b, 1700000000); err != nil {
		t.Fatal(err)
	}
	if x, y := sha256File(t, a), sha256File(t, b); x != y {
		t.Fatalf("two builds of the same tree differ:\n%s\n%s", x, y)
	}
}

func TestTarballIgnoresFilesystemTimes(t *testing.T) {
	src := tarballFixture(t)
	out := t.TempDir()
	a := filepath.Join(out, "a.tar.gz")
	if err := Tarball(src, "kno-v1", a, 1700000000); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(src, "server"), old, old); err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(out, "b.tar.gz")
	if err := Tarball(src, "kno-v1", b, 1700000000); err != nil {
		t.Fatal(err)
	}
	if x, y := sha256File(t, a), sha256File(t, b); x != y {
		t.Fatalf("a changed mtime changed the tarball:\n%s\n%s", x, y)
	}
}

func TestTarballGzipHeaderIsEmpty(t *testing.T) {
	src := tarballFixture(t)
	dst := filepath.Join(t.TempDir(), "kno.tar.gz")
	if err := Tarball(src, "kno-v1", dst, 1700000000); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	if zr.Name != "" {
		t.Errorf("gzip header carries a name: %q", zr.Name)
	}
	if !zr.ModTime.IsZero() && zr.ModTime.Unix() != 0 {
		t.Errorf("gzip header carries a timestamp: %v", zr.ModTime)
	}
}

func readTarball(t *testing.T, p string) []*tar.Header {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var hdrs []*tar.Header
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		hdrs = append(hdrs, h)
	}
	return hdrs
}

func TestTarballEntriesAreRootedSortedAndOwnerless(t *testing.T) {
	src := tarballFixture(t)
	dst := filepath.Join(t.TempDir(), "kno.tar.gz")
	if err := Tarball(src, "kno-v1", dst, 1700000000); err != nil {
		t.Fatal(err)
	}
	hdrs := readTarball(t, dst)
	var names []string
	for _, h := range hdrs {
		names = append(names, h.Name)
		if h.Uid != 0 || h.Gid != 0 {
			t.Errorf("%s: uid/gid %d/%d, want 0/0", h.Name, h.Uid, h.Gid)
		}
		if h.Uname != "" || h.Gname != "" {
			t.Errorf("%s: carries owner names %q/%q", h.Name, h.Uname, h.Gname)
		}
		if h.ModTime.Unix() != 1700000000 {
			t.Errorf("%s: mtime %d, want the epoch", h.Name, h.ModTime.Unix())
		}
	}
	want := []string{"kno-v1/README", "kno-v1/etc/mix.toml", "kno-v1/server"}
	if len(names) != len(want) {
		t.Fatalf("entries = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("entries = %v, want %v", names, want)
		}
	}
}

func TestTarballKeepsTheExecutableBit(t *testing.T) {
	src := tarballFixture(t)
	dst := filepath.Join(t.TempDir(), "kno.tar.gz")
	if err := Tarball(src, "kno-v1", dst, 1700000000); err != nil {
		t.Fatal(err)
	}
	for _, h := range readTarball(t, dst) {
		want := int64(0o644)
		if filepath.Base(h.Name) == "server" {
			want = 0o755
		}
		if h.Mode != want {
			t.Errorf("%s: mode %o, want %o", h.Name, h.Mode, want)
		}
	}
}

func TestTarballRefusesAnEmptyPrefix(t *testing.T) {
	src := tarballFixture(t)
	dst := filepath.Join(t.TempDir(), "kno.tar.gz")
	if err := Tarball(src, "", dst, 1700000000); err == nil {
		t.Fatal("an unprefixed tarball unpacks over the caller's cwd; want an error")
	}
}

func gitRepoFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.hooksPath",
			"GIT_CONFIG_VALUE_0=/dev/null",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e",
			"GIT_AUTHOR_DATE=2023-11-14T22:13:20+0000",
			"GIT_COMMITTER_DATE=2023-11-14T22:13:20+0000",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "one")
	run("tag", "v9.9.9")
	return dir
}

func TestSourceTarballIsDeterministic(t *testing.T) {
	repo := gitRepoFixture(t)
	out := t.TempDir()
	a := filepath.Join(out, "a.tar.gz")
	b := filepath.Join(out, "b.tar.gz")
	if err := SourceTarball(repo, "v9.9.9", "kno-9.9.9", a, 1700000000); err != nil {
		t.Fatal(err)
	}
	if err := SourceTarball(repo, "v9.9.9", "kno-9.9.9", b, 1700000000); err != nil {
		t.Fatal(err)
	}
	if x, y := sha256File(t, a), sha256File(t, b); x != y {
		t.Fatalf("two source tarballs of one tag differ:\n%s\n%s", x, y)
	}
	var found bool
	for _, h := range readTarball(t, a) {
		if h.Name == "kno-9.9.9/main.go" {
			found = true
		}
	}
	if !found {
		t.Error("source tarball does not carry kno-9.9.9/main.go")
	}
}

func TestSourceTarballRejectsAnUnknownTag(t *testing.T) {
	repo := gitRepoFixture(t)
	dst := filepath.Join(t.TempDir(), "x.tar.gz")
	if err := SourceTarball(repo, "v0.0.0-nope", "kno", dst, 1700000000); err == nil {
		t.Fatal("want an error for a tag that does not exist")
	}
	if _, err := os.Stat(dst); err == nil {
		t.Error("a failed source tarball left a file behind")
	}
}

func TestSourceTarballKeepsNoneOfGitsFraming(t *testing.T) {
	repo := gitRepoFixture(t)
	dst := filepath.Join(t.TempDir(), "src.tar.gz")
	if err := SourceTarball(repo, "v9.9.9", "kno-9.9.9", dst, 1700000000); err != nil {
		t.Fatal(err)
	}
	for _, h := range readTarball(t, dst) {
		switch h.Typeflag {
		case tar.TypeReg, tar.TypeSymlink:
		default:
			t.Errorf("%s: entry type %q survived from git's archive", h.Name, h.Typeflag)
		}
		if h.Uname != "" || h.Gname != "" {
			t.Errorf("%s: carries owner names %q/%q", h.Name, h.Uname, h.Gname)
		}
		if h.ModTime.Unix() != 1700000000 {
			t.Errorf("%s: mtime %d is not the epoch we passed", h.Name, h.ModTime.Unix())
		}
		if h.Format == tar.FormatPAX {
			t.Errorf("%s: written as PAX, not the USTAR we emit", h.Name)
		}
	}
}

func TestRepoHasNoGitattributesAffectingTheArchive(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, ".gitattributes"))
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"export-subst", "export-ignore"} {
		if strings.Contains(string(b), bad) {
			t.Errorf(".gitattributes sets %s, so the source tarball is no longer a"+
				" function of the tree alone; decide whether that is intended", bad)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skip("not in a git checkout")
	}
	return strings.TrimSpace(string(out))
}
