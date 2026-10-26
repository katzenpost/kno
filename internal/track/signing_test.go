package track

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gpgRepo(t *testing.T) (repo, signed, unsigned string) {
	t.Helper()
	for _, p := range []string{"git", "gpg"} {
		if _, err := exec.LookPath(p); err != nil {
			t.Skipf("%s not installed", p)
		}
	}
	home := t.TempDir()

	t.Setenv("GNUPGHOME", home)
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	gen := exec.Command("gpg", "--batch", "--quick-generate-key", "--passphrase", "",
		"kno test <kno@test.invalid>", "ed25519", "sign", "0")
	gen.Env = append(os.Environ(), "GNUPGHOME="+home)
	if b, err := gen.CombinedOutput(); err != nil {
		t.Skipf("cannot generate a test key here: %v\n%s", err, b)
	}

	repo = t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", repo}, args...)...)
		c.Env = append(os.Environ(), "GNUPGHOME="+home)
		if b, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	git("init", "-q")
	git("config", "user.name", "kno test")
	git("config", "core.hooksPath", "/dev/null")
	git("config", "user.email", "kno@test.invalid")
	git("config", "user.signingkey", "kno@test.invalid")
	git("config", "gpg.program", "gpg")
	git("commit", "-q", "--allow-empty", "-m", "base")
	git("tag", "-s", "-m", "signed release", "v1.0.0")
	git("commit", "-q", "--allow-empty", "-m", "next")
	git("tag", "v2.0.0")
	return repo, "v1.0.0", "v2.0.0"
}

func TestAnUnsignedTagIsRefusedWhenSignaturesAreRequired(t *testing.T) {
	repo, signed, unsigned := gpgRepo(t)
	e := Env{Repo: repo, VerifyTag: true}

	if !e.TagVerified(repo, signed) {
		t.Errorf("a signed tag was refused; the gate rejects everything")
	}
	if e.TagVerified(repo, unsigned) {
		t.Error("an UNSIGNED tag passed the signature gate")
	}

	if e.TagVerified(repo, "v9.9.9-nonexistent") {
		t.Error("a tag that does not exist passed the signature gate")
	}
}

func TestSignaturesAreNotConsultedWhenNotRequired(t *testing.T) {
	repo, signed, unsigned := gpgRepo(t)
	e := Env{Repo: repo}
	for _, tag := range []string{signed, unsigned, "v9.9.9-nonexistent"} {
		if !e.TagVerified(repo, tag) {
			t.Errorf("%s was refused with require_signed_tag off", tag)
		}
	}
}

func TestTargetSkipsAnUnsignedTagWhenSignaturesAreRequired(t *testing.T) {
	repo, signed, unsigned := gpgRepo(t)
	state := t.TempDir()
	e := Env{
		Repo: repo, KatzenpostRepo: repo, StateDir: state,
		QuarantineFile: filepath.Join(state, "quarantine"),
		CurrentFile:    filepath.Join(state, "current"),
		VerifyTag:      true,
	}

	writeVersions(t, repo, signed+"\n"+unsigned+"\n")

	got, why := e.Target()
	if got != signed {
		t.Fatalf("Target = %q (%s), want the signed %s rather than the newer %s",
			got, why, signed, unsigned)
	}

	e.VerifyTag = false
	if got, _ := e.Target(); got != unsigned {
		t.Errorf("Target = %q with the gate off, want the newest %s", got, unsigned)
	}
}

func TestATagSignedByAnUnknownKeyIsRefused(t *testing.T) {
	repo, signed, _ := gpgRepo(t)
	e := Env{Repo: repo, VerifyTag: true}
	if !e.TagVerified(repo, signed) {
		t.Fatal("the fixture's own signature does not verify")
	}

	empty := t.TempDir()
	if err := os.Chmod(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GNUPGHOME", empty)
	if e.TagVerified(repo, signed) {
		t.Error("a signature that cannot be checked was treated as verified")
	}
}

func TestATagThatLooksLikeAnOptionCannotBecomeOne(t *testing.T) {
	repo, _, _ := gpgRepo(t)
	e := Env{Repo: repo, VerifyTag: true}
	for _, tag := range []string{
		"--help",
		"--upload-pack=/bin/false",
		"-v",
	} {
		if e.TagVerified(repo, tag) {
			t.Errorf("%q was treated as a verified tag", tag)
		}
		if strings.HasPrefix(tag, "-") {

			if e.KPHasTag(tag) {
				t.Errorf("%q was treated as an existing katzenpost tag", tag)
			}
		}
	}
}

func TestARevisionTagIsVerifiedByItsUpstreamTag(t *testing.T) {
	repo, signed, unsigned := gpgRepo(t)
	e := Env{Repo: repo, VerifyTag: true}
	if !e.TagVerified(repo, signed+"-141") {
		t.Errorf("%s-141 was refused, but %s is signed", signed, signed)
	}
	if e.TagVerified(repo, unsigned+"-3") {
		t.Errorf("%s-3 passed, but %s is not signed", unsigned, unsigned)
	}
}

func TestASignedTagMustResolveToThePinnedCommit(t *testing.T) {
	repo, signed, _ := gpgRepo(t)
	data := t.TempDir()
	pins := filepath.Join(data, "thirdparty", "pins.env")
	if err := os.MkdirAll(filepath.Dir(pins), 0o755); err != nil {
		t.Fatal(err)
	}
	e := Env{Repo: data, VerifyTag: true}
	for _, c := range []struct {
		sha  string
		want bool
	}{{"0000000000000000000000000000000000000000", false}, {"", true}} {
		sha := c.sha
		if sha == "" {
			out, _ := GitOutput(repo, "rev-parse", signed+"^{commit}")
			sha = strings.TrimSpace(out)
		}
		body := "KATZENPOST_REF=" + signed + "\nKATZENPOST_SHA=" + sha + "\n"
		if err := os.WriteFile(pins, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := e.TagVerified(repo, signed); got != c.want {
			t.Errorf("pinned %s: verified %v, want %v", sha, got, c.want)
		}
	}
}
