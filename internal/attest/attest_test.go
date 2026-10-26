package attest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command(args[0], args[1:]...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@invalid",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
}

func key(t *testing.T, dir, name string) (priv, line string) {
	t.Helper()
	priv = filepath.Join(dir, name)
	run(t, dir, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", name, "-f", priv)
	pub, err := os.ReadFile(priv + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	return priv, name + " " + strings.TrimSpace(string(pub)) + "\n"
}

func fixture(t *testing.T) (repo, signers, alice, bob, carol string) {
	t.Helper()
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@invalid",
		"GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@invalid"} {
		t.Setenv(k, v)
	}
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("no ssh-keygen")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo = t.TempDir()
	run(t, repo, "git", "init", "-q")
	run(t, repo, "git", "commit", "-q", "--allow-empty", "-m", "base")
	keys := t.TempDir()
	alice, a := key(t, keys, "alice")
	bob, b := key(t, keys, "bob")
	carol, _ = key(t, keys, "carol")
	signers = filepath.Join(keys, "signers")
	if err := os.WriteFile(signers, []byte("# threshold 2\n"+a+b), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo, signers, alice, bob, carol
}

func TestTheReportFollowsTheSignatures(t *testing.T) {
	repo, signers, alice, bob, carol := fixture(t)
	if got := Check(repo, "HEAD", filepath.Join(t.TempDir(), "absent")); got.State != noSigners || got.Enough() {
		t.Errorf("no signer set: %+v", got)
	}
	if got := Check(repo, "HEAD", signers); got.State != unsigned || got.Enough() {
		t.Errorf("unsigned: %+v", got)
	}
	for _, k := range []string{alice, alice} {
		if _, err := Sign(repo, "HEAD", k); err != nil {
			t.Fatal(err)
		}
	}
	if got := Check(repo, "HEAD", signers); got.State != signed || got.Valid != 1 || got.Enough() {
		t.Errorf("one key twice must count once of threshold 2: %+v", got)
	}
	if _, err := Sign(repo, "HEAD", bob); err != nil {
		t.Fatal(err)
	}
	if got := Check(repo, "HEAD", signers); !got.Enough() || got.String() != "2 of 2 (alice, bob), threshold 2" {
		t.Errorf("two keys: %+v %s", got, got)
	}
	if _, err := Sign(repo, "HEAD", carol); err != nil {
		t.Fatal(err)
	}
	if got := Check(repo, "HEAD", signers); !got.Enough() || got.Bad != 1 ||
		got.String() != "2 of 2 (alice, bob), threshold 2, 1 bad signature(s)" {
		t.Errorf("a signature from outside the set is reported and counts for nothing: %+v %s", got, got)
	}
}

func TestASignatureCoversOnlyItsCommit(t *testing.T) {
	repo, signers, alice, bob, _ := fixture(t)
	for _, k := range []string{alice, bob} {
		if _, err := Sign(repo, "HEAD", k); err != nil {
			t.Fatal(err)
		}
	}
	run(t, repo, "git", "commit", "-q", "--allow-empty", "-m", "next")
	if got := Check(repo, "HEAD", signers); got.State != unsigned {
		t.Errorf("a new commit inherited the signatures of its parent: %+v", got)
	}
}

func TestABrokenSignerSetIsNotABadSignature(t *testing.T) {
	repo, signers, _, _, _ := fixture(t)
	b, _ := os.ReadFile(signers)
	os.WriteFile(signers, []byte(strings.Replace(string(b), "# threshold 2", "# threshold x", 1)), 0o644)
	got := Check(repo, "HEAD", signers)
	if got.State != badSet || got.Enough() || !strings.HasPrefix(got.String(), "bad signer set: ") {
		t.Errorf("a broken signer set: %+v %s", got, got)
	}
}
