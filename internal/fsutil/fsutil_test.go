package fsutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEveryPrivateKeyNameKatzenpostProducesIsRecognised(t *testing.T) {
	for _, name := range []string{

		"identity.private.pem",
		"link.private.pem",

		"id.sign_private.pem",
		"lk.kem_private.pem",

		"identity.private2.pem",
		"replica.private.pem",

		"./var/lib/pq-katzenpost-mixserver/identity.private.pem",
		"/var/lib/pq-katzenpost-authority/link.private.pem",

		"identity.PRIVATE.pem",
		"IDENTITY.PRIVATE.PEM",
		"id.sign_Private.pem",
	} {
		if !IsSecretKey(name) {
			t.Errorf("%q is a private key and was not recognised as one", name)
		}
	}
}

func TestThePublicKeysTheRepoShipsAreNotRefused(t *testing.T) {
	for _, name := range []string{
		"identity.public.pem",
		"oneeyedman_mix_id_pub_key.pem",
		"annares_provider_id_pub_key.pem",
		"wauland2_mix_id_pub.key.pem",
		"link.public.pem",
		"id.sign_public.pem",
		"lk.kem_public.pem",
		"katzenpost.toml",
		"thinclient.toml",
	} {
		if IsSecretKey(name) {
			t.Errorf("%q is public and was refused as a private key", name)
		}
	}
}

func TestFindSecretLooksEverywhere(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "keys", "mix-id-keys", "nested")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(root, "katzenpost.toml"),
		filepath.Join(root, "keys", "mixy_mix_id_pub_key.pem"),
	} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := FindSecret(root); err != nil || got != "" {
		t.Fatalf("a clean tree reported a secret: %s (%v)", got, err)
	}

	buried := filepath.Join(deep, "identity.private.pem")
	if err := os.WriteFile(buried, []byte("-----BEGIN PRIVATE KEY-----"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := FindSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Fatal("a private key three directories down was not found")
	}
	if !strings.HasSuffix(got, "identity.private.pem") {
		t.Errorf("FindSecret = %q, want the key it found", got)
	}
}

func TestCopyTreeGuardedRefusesRatherThanCopies(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "mixy_mix_id_pub_key.pem"), []byte("pub"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "out")
	if err := CopyTreeGuarded(src, dst); err != nil {
		t.Fatalf("a tree of public keys was refused: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "mixy_mix_id_pub_key.pem")); err != nil {
		t.Errorf("the public key was not copied: %v", err)
	}

	if err := os.WriteFile(filepath.Join(src, "identity.private.pem"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst2 := filepath.Join(t.TempDir(), "out2")
	err := CopyTreeGuarded(src, dst2)
	if err == nil {
		t.Fatal("a tree holding a private key was copied")
	}
	if !strings.Contains(err.Error(), "secret key") {
		t.Errorf("the refusal does not say what it found: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dst2, "identity.private.pem")); err == nil {
		t.Error("the private key was copied before the refusal")
	}
}

func TestASecretKeyIsFoundByItsBodyNotOnlyItsName(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	sneaky := write("evil_mix_id_key.pem", "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n")
	if !IsSecretKeyFile(sneaky) {
		t.Error("a private key under an innocent name was not recognised")
	}

	for _, hdr := range []string{
		"-----BEGIN RSA PRIVATE KEY-----",
		"-----BEGIN OPENSSH PRIVATE KEY-----",
		"-----BEGIN ED25519 SPHINCS+ PRIVATE KEY-----",
		"-----BEGIN ENCRYPTED PRIVATE KEY-----",
	} {
		end := strings.Replace(hdr, "BEGIN", "END", 1)
		p := write("innocent.pem", hdr+"\nAAAA\n"+end+"\n")
		if !IsSecretKeyFile(p) {
			t.Errorf("%q was not recognised as a private key", hdr)
		}
	}

	pub := write("mixy_mix_id_pub_key.pem", "-----BEGIN ED25519 SPHINCS+ PUBLIC KEY-----\nAAAA\n")
	if IsSecretKeyFile(pub) {
		t.Error("a public key was called secret")
	}

	if !IsSecretKeyFile(filepath.Join(dir, "identity.private.pem")) {
		t.Error("a secret NAME stopped being recognised when the file is absent")
	}
}

func TestASecretKeyIsFoundPastTheFirstBlockOfBytes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "innocent.pem")
	preamble := strings.Repeat("# nothing to see here\n", 60)
	body := preamble + "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if !IsSecretKeyFile(p) {
		t.Error("a private key behind a long preamble was not recognised")
	}

	q := filepath.Join(dir, "pub.pem")
	if err := os.WriteFile(q, []byte(preamble+
		"-----BEGIN ED25519 SPHINCS+ PUBLIC KEY-----\nAAAA\n-----END ED25519 SPHINCS+ PUBLIC KEY-----\n"),
		0o600); err != nil {
		t.Fatal(err)
	}
	if IsSecretKeyFile(q) {
		t.Error("a public key behind a long preamble was called secret")
	}
}

func TestCopyTreeGuardedLeavesFinderFilesBehind(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "out")
	for name, body := range map[string]string{"node.public.pem": "k", ".DS_Store": "finder"} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := CopyTreeGuarded(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, ".DS_Store")); err == nil {
		t.Error("a Finder .DS_Store was staged into a package")
	}
	if _, err := os.Stat(filepath.Join(dst, "node.public.pem")); err != nil {
		t.Errorf("the key was not staged: %v", err)
	}
}
