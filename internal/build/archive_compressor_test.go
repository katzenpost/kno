package build

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/release"
	"github.com/katzenpost/kno/internal/source"
	"github.com/katzenpost/kno/internal/testutil"
)

func TestSourceArchivesMatchCommittedDigests(t *testing.T) {
	if os.Getenv("KNO_TEST_SOURCE_ARCHIVES") != "1" {
		return
	}
	root := testutil.ModuleRoot(t)
	repo := filepath.Join(root, "data")
	pins, err := source.ParsePins(filepath.Join(repo, "thirdparty", "pins.env"))
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := strconv.ParseInt(CommittedEpoch(repo, "namenlos", pins["KATZENPOST_REF"]), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var paths []string
	want := map[string]string{}
	for _, p := range Recorded(CommittedDir(repo, "namenlos", pins["KATZENPOST_REF"])) {
		want[p.Name] = p.Sha256
	}
	for _, component := range []struct{ name, pin, suffix string }{
		{"katzenpost", "KATZENPOST_SHA", ""},
		{"hpqc", "HPQC_SHA", "-hpqc"},
	} {
		src := filepath.Join(t.TempDir(), component.name)
		for _, args := range [][]string{
			{"init", "-q", src},
			{"-C", src, "fetch", "-q", "--depth=1", "https://github.com/katzenpost/" + component.name, pins[component.pin]},
		} {
			if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		prefix := "katzenpost-" + strings.TrimPrefix(pins["KATZENPOST_REF"], "v") + component.suffix
		dst := filepath.Join(dir, prefix+"-src.tar.gz")
		if err := release.SourceTarball(src, pins[component.pin], prefix, dst, epoch); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(dst)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s before pinned compression: %x", filepath.Base(dst), sha256.Sum256(b))
		paths = append(paths, dst)
	}
	if err := normalizeArchiveCompression(dir, paths, pins); err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		got := hex.EncodeToString(sum[:])
		if expected := want[filepath.Base(p)]; expected == "" || got != expected {
			t.Errorf("%s: got %s, recorded %s", filepath.Base(p), got, expected)
		}
		t.Logf("%s: %s", filepath.Base(p), got)
	}
}

func TestArchiveCompressorPreservesTarBytesAndRecordedEncoding(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "compress.go")
	if err := os.WriteFile(helper, []byte(archiveCompressor), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "src")
	fakeBin(t, src)
	archive := filepath.Join(dir, "fixture.tar.gz")
	if err := release.Tarball(src, "fixture", archive, 1700000000); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	zr.Close()
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	zw, err := gzip.NewWriterLevel(out, gzip.NoCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	out.Close()
	cmd := exec.Command("go", "run", helper, filepath.Base(archive))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GO111MODULE=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compressor: %v\n%s", err, out)
	}
	got, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatal("recompression changed the established archive encoding")
	}
}
