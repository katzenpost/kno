package build

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/katzenpost/kno/internal/compile"
)

func fakeBin(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server"), []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestTarballsNameEveryPlatform(t *testing.T) {
	work := t.TempDir()
	rel := t.TempDir()
	pf := []compile.Platform{
		{Arch: "amd64", OS: "debian-13"},
		{Arch: "amd64", OS: "ubuntu-24.04"},
		{Arch: "arm64", OS: "debian-13"},
	}
	for _, p := range pf {
		fakeBin(t, binDir(work, p))
	}
	got, err := tarballs(tarballOpts{
		Repo: t.TempDir(), Tag: "v1.2.3", Work: work, Rel: rel, Platforms: pf,
	})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range got {
		names = append(names, filepath.Base(p))
	}
	sort.Strings(names)
	want := []string{
		"katzenpost-1.2.3-debian13-amd64.tar.gz",
		"katzenpost-1.2.3-debian13-arm64.tar.gz",
		"katzenpost-1.2.3-ubuntu24.04-amd64.tar.gz",
	}
	if len(names) != len(want) {
		t.Fatalf("tarballs = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("tarballs = %v, want %v", names, want)
		}
	}
	for _, p := range got {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was reported but not written: %v", filepath.Base(p), err)
		}
	}
}

func TestTarballsLandWithThePackages(t *testing.T) {
	work := t.TempDir()
	rel := t.TempDir()
	pf := []compile.Platform{{Arch: "amd64", OS: "debian-13"}}
	fakeBin(t, binDir(work, pf[0]))
	got, err := tarballs(tarballOpts{
		Repo: t.TempDir(), Tag: "v1.2.3", Work: work, Rel: rel, Platforms: pf,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range got {
		if d := filepath.Dir(p); d != filepath.Join(rel, "pkgs") {
			t.Errorf("%s landed in %s, want %s", filepath.Base(p), d, filepath.Join(rel, "pkgs"))
		}
	}
}

func TestTarballsSkipAnUncompiledPlatform(t *testing.T) {
	work := t.TempDir()
	rel := t.TempDir()
	pf := []compile.Platform{
		{Arch: "amd64", OS: "debian-13"},
		{Arch: "ppc64el", OS: "debian-13"},
	}
	fakeBin(t, binDir(work, pf[0]))
	got, err := tarballs(tarballOpts{
		Repo: t.TempDir(), Tag: "v1.2.3", Work: work, Rel: rel, Platforms: pf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d tarballs, want 1 (ppc64el was never compiled): %v", len(got), got)
	}
	if filepath.Base(got[0]) != "katzenpost-1.2.3-debian13-amd64.tar.gz" {
		t.Errorf("wrong tarball: %s", filepath.Base(got[0]))
	}
}

func TestTarballsIncludeFreeBSDWhenBuilt(t *testing.T) {
	work := t.TempDir()
	rel := t.TempDir()
	fakeBin(t, filepath.Join(work, "fbsd", "bin"))
	got, err := tarballs(tarballOpts{
		Repo: t.TempDir(), Tag: "v1.2.3", Work: work, Rel: rel, FreeBSDABI: "FreeBSD:14:amd64",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || filepath.Base(got[0]) != "katzenpost-1.2.3-freebsd-amd64.tar.gz" {
		t.Fatalf("got %v, want one katzenpost-1.2.3-freebsd-amd64.tar.gz", got)
	}
}

func TestTarballsSkipTheSourceWhenTheTagIsAbsent(t *testing.T) {
	work := t.TempDir()
	rel := t.TempDir()
	pf := []compile.Platform{{Arch: "amd64", OS: "debian-13"}}
	fakeBin(t, binDir(work, pf[0]))
	got, err := tarballs(tarballOpts{
		Repo: t.TempDir(), Tag: "v1.2.3", Work: work, Rel: rel, Platforms: pf, Source: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range got {
		if filepath.Base(p) == "kno-1.2.3-src.tar.gz" {
			t.Error("a source tarball was produced from a repo with no such tag")
		}
	}
}

func TestTarballsCarryTheClientConfig(t *testing.T) {
	work := t.TempDir()
	rel := t.TempDir()
	for _, n := range []string{"client.toml", "thinclient.toml"} {
		if err := os.WriteFile(filepath.Join(rel, n), []byte("x = 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pf := []compile.Platform{{Arch: "amd64", OS: "debian-13"}}
	fakeBin(t, binDir(work, pf[0]))
	got, err := tarballs(tarballOpts{
		Repo: t.TempDir(), Tag: "v1.2.3", Work: work, Rel: rel, Platforms: pf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %v", got)
	}
	names := tarEntryNames(t, got[0])
	for _, want := range []string{
		"katzenpost-1.2.3-debian13-amd64/etc/katzenpost/client.toml",
		"katzenpost-1.2.3-debian13-amd64/etc/katzenpost/thinclient.toml",
	} {
		if !names[want] {
			t.Errorf("tarball does not carry %s; it has %v", want, keys(names))
		}
	}
}

func TestTarballsSkipAbsentClientConfig(t *testing.T) {
	work := t.TempDir()
	rel := t.TempDir()
	pf := []compile.Platform{{Arch: "amd64", OS: "debian-13"}}
	fakeBin(t, binDir(work, pf[0]))
	got, err := tarballs(tarballOpts{
		Repo: t.TempDir(), Tag: "v1.2.3", Work: work, Rel: rel, Platforms: pf,
	})
	if err != nil {
		t.Fatalf("a release with no client config must still tarball: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %v", got)
	}
}

func tarEntryNames(t *testing.T, p string) map[string]bool {
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
	out := map[string]bool{}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		out[h.Name] = true
	}
	return out
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
