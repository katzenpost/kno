package sign

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSignAnArbitraryFile(t *testing.T) {
	needTools(t, "ssh-keygen")
	k := newKey(t, "alice")
	dir := t.TempDir()
	rel := filepath.Join(dir, "Release")
	if err := os.WriteFile(rel, []byte("Origin: kno\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	allow := allowlist(t, "alice", k.public)

	var out bytes.Buffer
	c := signCommand()
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs([]string{"--file", rel, "--key", k.private, "--allowed", allow})
	if err := c.Execute(); err != nil {
		t.Fatalf("signing a named file failed: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(rel + ".sig"); err != nil {
		t.Errorf("no signature beside the file: %v\n%s", err, out.String())
	}
}

func TestSignFilesUnderThePrincipalWhenSigsExists(t *testing.T) {
	needTools(t, "ssh-keygen")
	k := newKey(t, "alice")
	dir := t.TempDir()
	rel := filepath.Join(dir, "Release")
	if err := os.WriteFile(rel, []byte("Origin: kno\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sigs"), 0o755); err != nil {
		t.Fatal(err)
	}
	allow := allowlist(t, "alice", k.public)

	var out bytes.Buffer
	c := signCommand()
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs([]string{"--file", rel, "--key", k.private, "--allowed", allow})
	if err := c.Execute(); err != nil {
		t.Fatalf("sign: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "sigs", "alice.sig")); err != nil {
		t.Errorf("the signature was not filed under the signer: %v\n%s", err, out.String())
	}

	if _, err := os.Stat(rel + ".sig"); err == nil {
		t.Error("the signature was also left at Release.sig, where the next signer would overwrite it")
	}
}
