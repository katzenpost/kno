package sign

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type testKey struct{ private, public string }

func newKey(t *testing.T, name string) testKey {
	t.Helper()
	dir := t.TempDir()
	priv := filepath.Join(dir, name)
	out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", name, "-f", priv).CombinedOutput()
	if err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	b, err := os.ReadFile(priv + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	return testKey{private: priv, public: strings.TrimSpace(string(b))}
}

func allowlist(t *testing.T, pairs ...string) string {
	t.Helper()
	if len(pairs)%2 != 0 {
		t.Fatal("allowlist takes principal, key pairs")
	}
	var b strings.Builder
	for i := 0; i < len(pairs); i += 2 {
		b.WriteString(pairs[i] + " " + pairs[i+1] + "\n")
	}
	p := filepath.Join(t.TempDir(), "allowed_signers")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func mustRename(t *testing.T, from, to string) {
	t.Helper()
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
}

func needTools(t *testing.T, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, err := exec.LookPath(n); err != nil {
			t.Skipf("%s not available", n)
		}
	}
}

func buildDeb(t *testing.T, dir, name, version, arch string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "DEBIAN"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctl := "Package: " + name + "\nVersion: " + version + "\nArchitecture: " + arch +
		"\nMaintainer: t <t@example>\nDescription: test\n"
	if err := os.WriteFile(filepath.Join(root, "DEBIAN", "control"), []byte(ctl), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, name+"_"+version+"_"+arch+".deb")
	if b, err := exec.Command("dpkg-deb", "--build", root, out).CombinedOutput(); err != nil {
		t.Fatalf("dpkg-deb: %v\n%s", err, b)
	}
}
