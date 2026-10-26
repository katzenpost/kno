package track

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/attest"
)

func TestASpacedPinCannotSkipTheTagToPinCheck(t *testing.T) {
	repo, signed, _ := gpgRepo(t)
	data := t.TempDir()
	pins := filepath.Join(data, "thirdparty", "pins.env")
	if err := os.MkdirAll(filepath.Dir(pins), 0o755); err != nil {
		t.Fatal(err)
	}
	evil := "0000000000000000000000000000000000000000"
	body := "KATZENPOST_REF = " + signed + "\nKATZENPOST_SHA = " + evil + "\n"
	if err := os.WriteFile(pins, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if (Env{Repo: data, VerifyTag: true}).TagVerified(repo, signed) {
		t.Errorf("%s verified, but the build pins %s", signed, evil)
	}
}

func TestTheFastForwardGoesToTheCommitWhoseSignaturesWereChecked(t *testing.T) {
	for _, p := range []string{"git", "ssh-keygen"} {
		if _, err := exec.LookPath(p); err != nil {
			t.Skip(p)
		}
	}
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@invalid",
		"GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@invalid"} {
		t.Setenv(k, v)
	}
	keys := t.TempDir()
	priv := filepath.Join(keys, "op")
	if b, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", priv).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, b)
	}
	pub, _ := os.ReadFile(priv + ".pub")
	signers := filepath.Join(keys, "signers")
	os.WriteFile(signers, []byte("op "+string(pub)), 0o644)
	old := attest.SignersPath
	attest.SignersPath = signers
	defer func() { attest.SignersPath = old }()

	origin := t.TempDir()
	mkRepo(t, origin, "data/networks/n/editable-configs/manifest.toml", "[network]\n")
	home := t.TempDir()
	e := Env{Home: home, Repo: CloneDir(home), KnoURL: origin,
		KatzenpostRepo: filepath.Join(home, "katzenpost"), RequireSignatures: true}
	l := Logger{Tool: "t", W: &strings.Builder{}}
	if err := e.EnsureKnoRepo(l); err != nil {
		t.Fatal(err)
	}
	gitIn := func(dir string, args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	mkCommit(t, origin, "good", "signed\n")
	good := gitIn(origin, "rev-parse", "HEAD")
	if _, err := attest.Sign(origin, good, priv); err != nil {
		t.Fatal(err)
	}
	gitIn(origin, "checkout", "-q", "-b", "evil")
	mkCommit(t, origin, "evil", "unsigned\n")
	gitIn(origin, "checkout", "-q", "main")

	real, _ := exec.LookPath("git")
	bin := t.TempDir()
	clone := CloneDir(home)
	wrap := "#!/bin/sh\n\"" + real + "\" \"$@\"\nrc=$?\ncase \"$*\" in *\"kno-signatures show\"*)\n" +
		"  \"" + real + "\" -C \"" + clone + "\" update-ref refs/remotes/origin/main refs/remotes/origin/evil ;;\nesac\nexit $rc\n"
	os.WriteFile(filepath.Join(bin, "git"), []byte(wrap), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := e.EnsureKnoRepo(l); err != nil {
		t.Fatal(err)
	}
	if head := gitIn(clone, "rev-parse", "HEAD"); head != good {
		t.Errorf("checked %s, fast-forwarded to %s", good, head)
	}
}
