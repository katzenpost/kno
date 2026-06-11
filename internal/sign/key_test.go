package sign

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAGeneratedKeySignsAndVerifies(t *testing.T) {
	dir := t.TempDir()
	key, allowed, err := TestKey(dir)
	if err != nil {
		t.Skipf("could not generate a test key: %v", err)
	}
	sums := filepath.Join(dir, "SHA256SUMS")
	if err := os.WriteFile(sums, []byte("aaa  katzenpost_1_amd64.deb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sig, err := Sign(sums, key)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Verify(sums, sig, allowed)
	if err != nil {
		t.Fatalf("a release signed by the generated key did not verify: %v", err)
	}
	if res.Principal != TestKeyName {
		t.Errorf("principal = %q, want %q", res.Principal, TestKeyName)
	}

	if allowed == NodePath {
		t.Fatal("the generated allowlist was written to the enforcing path")
	}
	if !strings.HasPrefix(allowed, dir) {
		t.Errorf("the allowlist escaped the test directory: %s", allowed)
	}
}

func TestDefaultKeyPicksOnlyAnAllowedKey(t *testing.T) {
	home := t.TempDir()
	ssh := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	t.Setenv("SSH_AUTH_SOCK", "")

	gen := func(name string) string {
		d := t.TempDir()
		k, _, err := TestKey(d)
		if err != nil {
			t.Skipf("could not generate: %v", err)
		}
		b, err := os.ReadFile(k)
		if err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(ssh, name+".pub")
		if err := os.WriteFile(dst, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return dst
	}
	stranger := gen("id_stranger")
	mine := gen("id_mine")

	body, err := os.ReadFile(mine)
	if err != nil {
		t.Fatal(err)
	}
	allowed := filepath.Join(home, "allowed_signers")
	if err := os.WriteFile(allowed,
		[]byte("someone@example.invalid "+strings.TrimSpace(string(body))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := DefaultKey(allowed)
	if err != nil {
		t.Fatalf("DefaultKey: %v", err)
	}
	if got.Path != mine {
		t.Errorf("chose %s, want the allowed key %s", got.Path, mine)
	}
	if got.Path == stranger {
		t.Error("chose a key the allowlist does not grant")
	}
	if got.Principal != "someone@example.invalid" {
		t.Errorf("principal = %q", got.Principal)
	}

	empty := filepath.Join(home, "empty_signers")
	if err := os.WriteFile(empty, []byte("# nobody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := DefaultKey(empty); err == nil {
		t.Error("an allowlist granting nobody produced a signing key")
	}

	other := filepath.Join(home, "other_signers")
	sb, err := os.ReadFile(stranger)
	if err != nil {
		t.Fatal(err)
	}
	_ = sb
	if err := os.WriteFile(other,
		[]byte("nobody@example.invalid ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\n"),
		0o600); err != nil {
		t.Fatal(err)
	}
	_, err = DefaultKey(other)
	if err == nil {
		t.Fatal("signed with a key the allowlist does not grant")
	}
	if !strings.Contains(err.Error(), "nobody@example.invalid") {
		t.Errorf("the refusal does not say who is granted: %v", err)
	}
}
