package build

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/pkg/freebsd"
)

func TestFreeBSDKeepsRecordedVersionOnlyForIdenticalPackage(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1600000000")
	repo, rel, bin := t.TempDir(), t.TempDir(), t.TempDir()
	fakeBin(t, bin)
	client := filepath.Join(rel, "client.toml")
	if err := os.WriteFile(client, []byte("client = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const stamp = "+20261029223246"
	original, err := freebsd.PackageStamped(bin, rel, "v0.0.107", "", repo, freebsd.DefaultABI, stamp)
	if err != nil {
		t.Fatal(err)
	}
	sum, _, err := fsutil.SHA256File(original)
	if err != nil {
		t.Fatal(err)
	}
	record := CommittedDir(repo, "namenlos", "v0.0.107")
	if err := os.MkdirAll(record, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(record, SumsName), []byte(fmt.Sprintf("%s  %s\n", sum, filepath.Base(original))), 0o644); err != nil {
		t.Fatal(err)
	}
	o := Options{Repo: repo, Network: "namenlos", Tag: "v0.0.107"}
	build := func() string {
		t.Helper()
		p, err := freebsdPackage(o, bin, rel, freebsd.DefaultABI)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	if got := build(); got != original {
		t.Fatalf("identical package changed name: %s", got)
	}
	if got := freebsdBinaryStamp(o, original); got != stamp {
		t.Fatalf("config dependency stamp %s, want %s", got, stamp)
	}
	for _, change := range []struct{ path, body string }{
		{filepath.Join(bin, "server"), "new binary"},
		{client, "client = 2\n"},
	} {
		before, err := os.ReadFile(change.path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(change.path, []byte(change.body), 0o755); err != nil {
			t.Fatal(err)
		}
		if got := build(); got == original {
			t.Fatalf("changed input %s reused recorded name", change.path)
		}
		if got, _, err := fsutil.SHA256File(original); err != nil || got != sum {
			t.Fatalf("mismatching candidate overwrote recorded package: %s, %v", got, err)
		}
		if err := os.WriteFile(change.path, before, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}
