package attest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const junk = "-----BEGIN SSH SIGNATURE-----\nU1NIU0lH\n-----END SSH SIGNATURE-----\n"

func TestOneJunkNoteCannotVetoEnoughValidSignatures(t *testing.T) {
	repo, signers, alice, bob, _ := fixture(t)
	for _, k := range []string{alice, bob} {
		if _, err := Sign(repo, "HEAD", k); err != nil {
			t.Fatal(err)
		}
	}
	run(t, repo, "git", "notes", "--ref="+NotesRef, "append", "-m", junk, "HEAD")
	if got := Check(repo, "HEAD", signers); !got.Enough() {
		t.Errorf("2 valid of threshold 2 plus one junk block: %+v", got)
	}
}

func TestTheVerifyWorkIsBounded(t *testing.T) {
	repo, signers, _, _, _ := fixture(t)
	run(t, repo, "git", "notes", "--ref="+NotesRef, "add", "-m", strings.Repeat(junk, 200), "HEAD")
	real, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip(err)
	}
	bin := t.TempDir()
	count := filepath.Join(bin, "count")
	os.WriteFile(filepath.Join(bin, "ssh-keygen"),
		[]byte("#!/bin/sh\necho x >> \""+count+"\"\nexec \""+real+"\" \"$@\"\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	Check(repo, "HEAD", signers)
	b, _ := os.ReadFile(count)
	if n := strings.Count(string(b), "x"); n > 32 {
		t.Errorf("200 junk blocks ran ssh-keygen %d times", n)
	}
}

func TestAThresholdAboveTheSignersIsRefused(t *testing.T) {
	_, signers, _, _, _ := fixture(t)
	b, _ := os.ReadFile(signers)
	os.WriteFile(signers, []byte(strings.Replace(string(b), "# threshold 2", "# threshold 3", 1)), 0o644)
	if k, n, err := threshold(signers); err == nil {
		t.Errorf("threshold %d of %d signers was accepted", k, n)
	}
}
