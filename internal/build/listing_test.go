package build

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestTheBuildListsEachPackageWithItsHash(t *testing.T) {
	p := filepath.Join(t.TempDir(), "katzenpost_0.0.107+debian13_amd64.deb")
	if err := os.WriteFile(p, []byte("deb"), 0o644); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := listBuilt(&b, []string{p}); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("deb"))
	if want := hex.EncodeToString(sum[:]) + "  " + p + "\n"; b.String() != want {
		t.Errorf("listing = %q, want %q", b.String(), want)
	}
}

func TestTheListIsSortedByPath(t *testing.T) {
	dir := t.TempDir()
	var pkgs []string
	for _, n := range []string{"z.deb", "a.deb"} {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
		pkgs = append(pkgs, p)
	}
	var b bytes.Buffer
	if err := listBuilt(&b, pkgs); err != nil {
		t.Fatal(err)
	}
	out := b.Bytes()
	if bytes.Index(out, []byte("a.deb")) > bytes.Index(out, []byte("z.deb")) {
		t.Errorf("the list is not sorted:\n%s", b.String())
	}
}
