package activate

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/build"
	"github.com/katzenpost/kno/internal/track"
)

func TestRootChecksEachPackageAgainstTheRecord(t *testing.T) {
	repo := t.TempDir()
	pkgs := filepath.Join(t.TempDir(), "pkgs")
	os.MkdirAll(pkgs, 0o755)
	name := "katzenpost_0.0.107+debian13_amd64.deb"
	os.WriteFile(filepath.Join(pkgs, name), []byte("built"), 0o644)
	o := Options{Repo: repo, Network: "namenlos", Tag: "v0.0.107"}
	var out strings.Builder
	l := track.Logger{Tool: "t", W: &out}
	if rc := checkRecord(l, o, pkgs, false, ""); rc != 0 {
		t.Errorf("no record and no require_signatures: rc %d\n%s", rc, out.String())
	}
	if rc := checkRecord(l, o, pkgs, true, ""); rc == 0 {
		t.Error("no record, and require_signatures is on: the install went ahead")
	}
	rel := build.CommittedDir(repo, "namenlos", "v0.0.107")
	os.MkdirAll(rel, 0o755)
	other := sha256.Sum256([]byte("recorded"))
	os.WriteFile(filepath.Join(rel, build.SumsName), []byte(hex.EncodeToString(other[:])+"  "+name+"\n"), 0o644)
	if rc := checkRecord(l, o, pkgs, false, ""); rc == 0 {
		t.Error("a package that does not match the record was installed")
	}
	os.WriteFile(filepath.Join(rel, build.SumsName), []byte(""), 0o644)
	if rc := checkRecord(l, o, pkgs, false, ""); rc != 0 {
		t.Errorf("an unnamed package without require_signatures: rc %d", rc)
	}
	if rc := checkRecord(l, o, pkgs, true, ""); rc == 0 {
		t.Error("an unnamed package with require_signatures was installed")
	}
}

func TestRootCopiesNoSymlink(t *testing.T) {
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "a.deb"), []byte("x"), 0o644)
	if err := os.Symlink("/etc/shadow", filepath.Join(src, "b.deb")); err != nil {
		t.Fatal(err)
	}
	if dir, err := ownCopy(src); err == nil {
		os.RemoveAll(dir)
		t.Error("the copy followed a symlink into the release")
	}
}

func TestRootVerifiesOnlyPackagesSelectedForInstallation(t *testing.T) {
	repo := t.TempDir()
	dir := t.TempDir()
	name := "katzenpost_0.0.107+debian13_amd64.deb"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("current"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "katzenpost-0.0.107-debian13-amd64.tar.gz"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := build.CommittedDir(repo, "namenlos", "v0.0.107")
	if err := os.MkdirAll(rec, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("current"))
	if err := os.WriteFile(filepath.Join(rec, build.SumsName), []byte(hex.EncodeToString(sum[:])+"  "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	l := track.Logger{Tool: "activate", W: &out}
	o := Options{Repo: repo, Network: "namenlos", Tag: "v0.0.107"}
	if rc := checkRecord(l, o, dir, true, "", path); rc != 0 {
		t.Fatalf("obsolete unselected cache blocked activation: %s", out.String())
	}
	if err := os.WriteFile(path, []byte("corrupted"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rc := checkRecord(l, o, dir, true, "", path); rc == 0 {
		t.Fatal("corrupted selected package passed root verification")
	}
}
