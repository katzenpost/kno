package sign

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func signer(t *testing.T, dir, principal string) (key, allowed string) {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("no ssh-keygen")
	}
	key = filepath.Join(dir, principal)
	cmd := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", principal, "-f", key)
	cmd.Stdin = strings.NewReader("")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("could not make a key: %v: %s", err, b)
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	allowed = filepath.Join(dir, "allowed_signers")
	if err := os.WriteFile(allowed, []byte(principal+" "+string(pub)), 0o644); err != nil {
		t.Fatal(err)
	}
	return key, allowed
}

func sums(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "SHA256SUMS")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSignAndVerify(t *testing.T) {
	dir := t.TempDir()
	key, allowed := signer(t, dir, "jacob")
	s := sums(t, dir, "aaa  katzenpost_1_amd64.deb\n")

	sig, err := Sign(s, key+".pub")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Verify(s, sig, allowed)
	if err != nil {
		t.Fatalf("a signature this test just made did not verify: %v", err)
	}
	if res.Principal != "jacob" {
		t.Errorf("principal = %q, want jacob", res.Principal)
	}
}

func TestVerifyFailsClosed(t *testing.T) {
	dir := t.TempDir()
	key, allowed := signer(t, dir, "jacob")
	s := sums(t, dir, "aaa  katzenpost_1_amd64.deb\n")
	sig, err := Sign(s, key+".pub")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("the packages changed after signing", func(t *testing.T) {
		tampered := filepath.Join(t.TempDir(), "SHA256SUMS")
		if err := os.WriteFile(tampered, []byte("bbb  katzenpost_1_amd64.deb\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Verify(tampered, sig, allowed); err == nil {
			t.Error("a release whose digests changed after signing verified")
		}
	})

	t.Run("the signer is not in the allowlist", func(t *testing.T) {
		other := filepath.Join(t.TempDir(), "allowed_signers")
		_, someone := signer(t, t.TempDir(), "someone-else")
		b, err := os.ReadFile(someone)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(other, b, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Verify(s, sig, other); err == nil {
			t.Error("a release signed by a key the host does not trust verified")
		}
	})

	t.Run("there is no signature at all", func(t *testing.T) {
		if _, err := Verify(s, filepath.Join(dir, "absent.sig"), allowed); err == nil {
			t.Error("an unsigned release verified")
		}
	})

	t.Run("the allowlist grants nobody", func(t *testing.T) {
		empty := filepath.Join(t.TempDir(), "allowed_signers")
		if err := os.WriteFile(empty, []byte("# nobody\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Verify(s, sig, empty); err == nil {
			t.Error("an allowlist naming nobody accepted a release")
		}
	})

	t.Run("the allowlist is missing", func(t *testing.T) {
		if _, err := Verify(s, sig, filepath.Join(t.TempDir(), "nope")); err == nil {
			t.Error("a missing allowlist accepted a release")
		}
	})
}

func TestASignatureFromAnotherNamespaceDoesNotVerify(t *testing.T) {
	dir := t.TempDir()
	key, allowed := signer(t, dir, "jacob")
	s := sums(t, dir, "aaa  katzenpost_1_amd64.deb\n")

	cmd := exec.Command("ssh-keygen", "-Y", "sign", "-n", "git", "-f", key+".pub", s)
	cmd.Stdin = strings.NewReader("")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("could not sign in another namespace: %v: %s", err, b)
	}
	if _, err := Verify(s, s+".sig", allowed); err == nil {
		t.Error("a signature made for another purpose authorized a release")
	}
}

func TestResigningReplacesTheSignature(t *testing.T) {
	dir := t.TempDir()
	key, allowed := signer(t, dir, "jacob")
	s := sums(t, dir, "aaa  katzenpost_1_amd64.deb\n")
	if _, err := Sign(s, key+".pub"); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(s, []byte("bbb  katzenpost_2_amd64.deb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Sign(s, key+".pub"); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(s, s+".sig", allowed); err != nil {
		t.Errorf("the second signature did not replace the first: %v", err)
	}
}

func TestPrincipals(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "allowed_signers")
	body := "# a comment\n\n" +
		"jacob ssh-ed25519 AAAA\n" +
		"alice,bob ssh-ed25519 BBBB\n" +
		`"quoted,list" ssh-ed25519 CCCC` + "\n" +
		"malformed\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Principals(p)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"jacob", "alice", "bob"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Principals = %v, want %v", got, want)
	}

	for _, g := range got {
		if strings.Contains(g, "quoted") {
			t.Errorf("parsed a quoted principal list: %v", got)
		}
	}
}
