package activate

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/katzenpost/kno/internal/build"
	"github.com/katzenpost/kno/internal/track"
)

func TestAnUntrackedRecordIsNotPartOfTheSignedTree(t *testing.T) {
	repo := t.TempDir()
	git := func(a ...string) string {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@t",
			"-c", "core.hooksPath=/dev/null"}, a...)...)
		b, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s", err, b)
		}
		return string(b)
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "a"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "a")
	git("commit", "-q", "-m", "a")
	sha, _ := track.GitOutput(repo, "rev-parse", "HEAD")
	pkgs := filepath.Join(t.TempDir(), "pkgs")
	name := "katzenpost_0.0.108+debian13_amd64.deb"
	if err := os.MkdirAll(pkgs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgs, name), []byte("evil"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("evil"))
	rec := build.CommittedDir(repo, "namenlos", "v0.0.108")
	if err := os.MkdirAll(rec, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rec, build.SumsName), []byte(hex.EncodeToString(sum[:])+"  "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := Options{Repo: repo, Network: "namenlos", Tag: "v0.0.108"}
	l := track.Logger{Tool: "t", W: io.Discard}
	if rc := checkRecord(l, o, pkgs, true, sha); rc == 0 {
		t.Fatal("an untracked record that the signed commit does not have let root install")
	}
	git("add", ".")
	git("commit", "-q", "-m", "record")
	sha, _ = track.GitOutput(repo, "rev-parse", "HEAD")
	if rc := checkRecord(l, o, pkgs, true, sha); rc != 0 {
		t.Fatal("a record in the signed commit did not count")
	}
}
