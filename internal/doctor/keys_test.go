package doctor

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const pubPEM = "-----BEGIN ED25519 SPHINCS+ PUBLIC KEY-----\nQUJD\n-----END ED25519 SPHINCS+ PUBLIC KEY-----\n"
const privPEM = "-----BEGIN ED25519 SPHINCS+ PRIVATE KEY-----\nQUJD\n-----END ED25519 SPHINCS+ PRIVATE KEY-----\n"

func keyDir(t *testing.T, files map[string]string) string {
	t.Helper()
	d := t.TempDir()
	writeKeys(t, d, files)
	return d
}

func writeKeys(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for n, body := range files {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func oldScan(stateDir string) []KeyMismatch {
	var out []KeyMismatch
	_ = filepath.WalkDir(stateDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".pem") {
			return nil
		}
		want := expectedKind(d.Name())
		if want == "" {
			return nil
		}
		h := ""
		if f, e := os.Open(p); e == nil {
			s := bufio.NewScanner(f)
			for s.Scan() {
				l := strings.TrimSpace(s.Text())
				if strings.HasPrefix(l, "-----BEGIN ") {
					h = strings.TrimSuffix(strings.TrimPrefix(l, "-----BEGIN "), "-----")
					break
				}
			}
			f.Close()
		}
		if !strings.Contains(h, want) {
			out = append(out, KeyMismatch{Path: p, Header: h, Expect: want})
		}
		return nil
	})
	return out
}

const oneeyedmanConfig = `[Server]
  Identifier = "oneeyedman"
  PKISignatureScheme = "Ed25519 Sphincs+"
  WireKEM = "KYBER768-X25519"
  Addresses = ["tcp://64.187.167.230:49278"]
  DataDir = "%s"
  IsGatewayNode = false
  IsServiceNode = false

[Logging]
  Disable = false
  Level = "NOTICE"

[PKI]
  [PKI.Voting]
    [[PKI.Voting.Authorities]]
      Identifier = "annares"
      PKISignatureScheme = "Ed25519 Sphincs+"
      IdentityPublicKey = """%s"""
`

func oneeyedman(t *testing.T, authorityKey string) (string, string) {
	t.Helper()
	tmp := t.TempDir()
	state := filepath.Join(tmp, "var", "lib", "pq-katzenpost-mixserver")
	writeKeys(t, state, map[string]string{

		"identity.public.pem":  privPEM,
		"identity.private.pem": privPEM,
		"link.public.pem":      "-----BEGIN KYBER768-X25519 PUBLIC KEY-----\nQUJD\n-----END KYBER768-X25519 PUBLIC KEY-----\n",
		"link.private.pem":     "-----BEGIN KYBER768-X25519 PRIVATE KEY-----\nQUJD\n-----END KYBER768-X25519 PRIVATE KEY-----\n",

		"oneeyedman_mix_id_pub_key.pem": pubPEM,
		"mixy_mix_id_pub_key.pem":       pubPEM,
	})
	cfg := filepath.Join(tmp, "etc", "katzenpost.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if authorityKey == "" {
		authorityKey = pubPEM
	}
	body := fmt.Sprintf(oneeyedmanConfig, state, authorityKey)
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return state, cfg
}

func TestOneeyedmanKeyFaultIsFound(t *testing.T) {
	cases := []struct {
		name string

		arrange  func(t *testing.T, state string) string
		rootOnly bool
	}{
		{

			name:    "a plain state directory",
			arrange: func(_ *testing.T, state string) string { return state },
		},
		{

			name: "the state directory is a symlink",
			arrange: func(t *testing.T, state string) string {
				link := filepath.Join(t.TempDir(), "state-link")
				if err := os.Symlink(state, link); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				return link
			},
		},
		{

			name:     "the state directory will not list",
			rootOnly: true,
			arrange: func(t *testing.T, state string) string {
				if err := os.Chmod(state, 0o111); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(state, 0o755) })
				return state
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.rootOnly && os.Geteuid() == 0 {
				t.Skip("root ignores directory permissions")
			}
			state, cfg := oneeyedman(t, "")
			scanDir := c.arrange(t, state)

			got := ScanKeys(scanDir, cfg)
			var found *KeyMismatch
			for i := range got.Mismatches {
				if filepath.Base(got.Mismatches[i].Path) == "identity.public.pem" {
					found = &got.Mismatches[i]
				}
			}
			if found == nil {
				t.Fatalf("the file the daemon refuses was not named.\nscanned: %s\nmismatches: %v\nblocked: %v",
					got.Coverage(), got.Mismatches, got.Blocked)
			}
			if found.Expect != "PUBLIC" || !strings.Contains(found.Header, "PRIVATE") {
				t.Errorf("described wrong: %+v", *found)
			}
			if c.name == "a plain state directory" {
				return
			}

			if old := oldScan(scanDir); len(old) != 0 {
				t.Errorf("the old scan was supposed to miss this, so the case"+
					" no longer reproduces the miss: %v", old)
			}
		})
	}
}

func TestScanFollowsTheConfigsDataDirNotOnlyTheStateDir(t *testing.T) {
	state, cfg := oneeyedman(t, "")

	elsewhere := t.TempDir()
	writeKeys(t, elsewhere, map[string]string{"identity.public.pem": pubPEM})
	if old := oldScan(elsewhere); len(old) != 0 {
		t.Fatalf("the decoy directory is not clean: %v", old)
	}

	got := ScanKeys(elsewhere, cfg)
	if got.DataDir != state {
		t.Fatalf("DataDir = %q, want %q", got.DataDir, state)
	}
	if !hasPath(got.Mismatches, filepath.Join(state, "identity.public.pem")) {
		t.Errorf("the file the daemon actually reads was never looked at.\nscanned: %s\ngot: %v",
			got.Coverage(), got.Mismatches)
	}
}

func TestConfigReferencedPemResolvesAgainstDataDir(t *testing.T) {
	tmp := t.TempDir()
	data := filepath.Join(tmp, "data")
	writeKeys(t, data, map[string]string{"peer_mix_id_pub_key.pem": privPEM})
	cfg := filepath.Join(tmp, "authority.toml")
	body := fmt.Sprintf("[Server]\n  DataDir = %q\n\n[[Mixes]]\n  IdentityPublicKeyPem = \"peer_mix_id_pub_key.pem\"\n", data)
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ScanKeys("", cfg)
	want := filepath.Join(data, "peer_mix_id_pub_key.pem")
	named := false
	for _, m := range got.Mismatches {
		if m.Path == want && m.Field == "IdentityPublicKeyPem" {
			named = true
		}
	}
	if !named {
		t.Fatalf("the referenced key was not checked, or the report does not say which"+
			" config field names it: %v (scanned %s)", got.Mismatches, got.Coverage())
	}

	if n := len(got.Mismatches); n != 1 {
		t.Errorf("reported the same file %d times: %v", n, got.Mismatches)
	}

	missing := filepath.Join(tmp, "gone.toml")
	body = fmt.Sprintf("[Server]\n  DataDir = %q\n\n[[Mixes]]\n  IdentityPublicKeyPem = \"absent_pub_key.pem\"\n", data)
	if err := os.WriteFile(missing, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if b := ScanKeys("", missing).Blocked; len(b) == 0 {
		t.Error("a config naming a key that is not there reported nothing")
	}
}

func TestInlinePemBlockInTheConfigIsChecked(t *testing.T) {
	_, cfg := oneeyedman(t, privPEM)
	got := ScanKeys("", cfg)
	var m *KeyMismatch
	for i := range got.Mismatches {
		if got.Mismatches[i].Field == "IdentityPublicKey" {
			m = &got.Mismatches[i]
		}
	}
	if m == nil {
		t.Fatalf("a private key embedded in IdentityPublicKey was not found: %v", got.Mismatches)
	}
	if m.Path != cfg || m.Expect != "PUBLIC" || !strings.Contains(m.Header, "PRIVATE") {
		t.Errorf("described wrong: %+v", *m)
	}

	clean := filepath.Join(t.TempDir(), "clean.toml")
	body := fmt.Sprintf("[Server]\n  DataDir = %q\n\n[[PKI.Voting.Authorities]]\n"+
		"  IdentityPublicKey = \"\"\"%s\"\"\"\n", t.TempDir(), pubPEM)
	if err := os.WriteFile(clean, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ScanKeys("", clean); len(got.Mismatches) != 0 {
		t.Errorf("a correct inline public key was flagged: %v", got.Mismatches)
	}
}

func TestAnUnreadableDirectoryIsReportedNotSwallowed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	d := keyDir(t, map[string]string{"identity.public.pem": privPEM})
	if err := os.Chmod(d, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(d, 0o755) })

	if old := oldScan(d); len(old) != 0 {
		t.Fatalf("this case no longer reproduces the silent miss: %v", old)
	}
	got := ScanKeys(d, "")
	if len(got.Blocked) == 0 {
		t.Fatalf("an unreadable state dir reported nothing at all: %+v", got)
	}
	if got.Scanned != 0 || !strings.Contains(got.Coverage(), "0 PEM file(s)") {
		t.Errorf("coverage does not admit it read nothing: %s", got.Coverage())
	}
}

func TestKeyMismatchFindsAPrivateKeyInAPublicFile(t *testing.T) {
	d := keyDir(t, map[string]string{
		"identity.public.pem":  privPEM,
		"identity.private.pem": privPEM,
		"link.public.pem":      pubPEM,
	})
	got := ScanKeys(d, "").Mismatches
	if len(got) != 1 {
		t.Fatalf("got %d mismatches, want 1: %v", len(got), got)
	}
	if !strings.HasSuffix(got[0].Path, "identity.public.pem") {
		t.Errorf("named the wrong file: %s", got[0].Path)
	}
	if got[0].Expect != "PUBLIC" || !strings.Contains(got[0].Header, "PRIVATE") {
		t.Errorf("mismatch described wrong: %+v", got[0])
	}
}

func TestKeyMismatchFindsAPublicKeyInAPrivateFile(t *testing.T) {
	d := keyDir(t, map[string]string{"identity.private.pem": pubPEM})
	got := ScanKeys(d, "").Mismatches
	if len(got) != 1 || got[0].Expect != "PRIVATE" {
		t.Fatalf("got %v, want one PRIVATE mismatch", got)
	}
}

func TestKeyMismatchUnderstandsTheRepoNaming(t *testing.T) {
	d := keyDir(t, map[string]string{
		"oneeyedman_mix_id_pub_key.pem": pubPEM,
		"storage_replica_0.public.pem":  pubPEM,
	})
	if got := ScanKeys(d, "").Mismatches; len(got) != 0 {
		t.Errorf("flagged correct files: %v", got)
	}
}

func TestKeyMismatchIsQuietWhenCorrect(t *testing.T) {
	d := keyDir(t, map[string]string{
		"identity.public.pem":  pubPEM,
		"identity.private.pem": privPEM,
		"persistence.db":       "not a pem",
		"unknown.pem":          pubPEM,
	})
	if got := ScanKeys(d, "").Mismatches; len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}

func TestKeyMismatchFlagsAFileThatIsNotPEM(t *testing.T) {
	d := keyDir(t, map[string]string{"identity.public.pem": "garbage\n"})
	got := ScanKeys(d, "").Mismatches
	if len(got) != 1 || got[0].Header != "" {
		t.Fatalf("got %v, want one not-a-PEM finding", got)
	}
}

func TestKeyMismatchToleratesAMissingDirectory(t *testing.T) {
	if got := ScanKeys(filepath.Join(t.TempDir(), "nope"), "").Mismatches; len(got) != 0 {
		t.Errorf("got %v for a directory that is not there", got)
	}
}

func TestKeyReportNeverSaysRegenerate(t *testing.T) {
	state, cfg := oneeyedman(t, "")
	k := ScanKeys(state, cfg)
	if len(k.Mismatches) == 0 {
		t.Fatalf("the fixture stopped reproducing the fault: %+v", k)
	}
	var b bytes.Buffer
	r := newResult(&b, false)
	reportKeys(r, "oneeyedman-pq-mixserver", k)
	out := b.String()

	for _, forbidden := range []string{
		"rm ", "rm -", "delete it", "regenerate it", "genkeypair",
		"shred", "truncate", "mv ", "> /var/lib", "--force",
	} {
		if strings.Contains(out, forbidden) {
			t.Errorf("the report suggests %q, which can destroy a node:\n%s", forbidden, out)
		}
	}
	for _, want := range []string{
		"DO NOT delete",
		"identity.public.pem",
		"kno backup-keys",
		"wrong key type",

		"NO PACKAGE OWNS THESE FILES",
		"will not fix this",

		"sha256sum",
		"DIFFERENT hashes",
		"Ask the network operators for help",

		"oneeyedman_mix_id_pub_key.pem",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report is missing %q:\n%s", want, out)
		}
	}

	if strings.Contains(out, "mixy_mix_id_pub_key.pem") {
		t.Errorf("the report offers another node's key as the replacement:\n%s", out)
	}
}

func TestKeyGapFiresWhenTheDaemonComplainsAndTheScanDidNot(t *testing.T) {
	k := ScanKeys(t.TempDir(), "")
	w := unitWhy{Log: []string{
		"ERROR",
		"Failed to spawn server instance: attempted to decode PEM file with wrong key type" +
			" ED25519 SPHINCS+ PRIVATE KEY != ED25519 SPHINCS+ PUBLIC KEY.",
	}}
	var b bytes.Buffer
	r := newResult(&b, false)
	reportKeyGap(r, "oneeyedman-pq-mixserver", k, w)
	if !r.failed() {
		t.Error("doctor passed a node whose daemon is refusing a key file")
	}
	for _, want := range []string{"found nothing wrong", "scanned:", "identity.public.pem", "backup-keys"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q in:\n%s", want, b.String())
		}
	}

	state, cfg := oneeyedman(t, "")
	var q bytes.Buffer
	qr := newResult(&q, false)
	reportKeyGap(qr, "oneeyedman-pq-mixserver", ScanKeys(state, cfg), w)
	if q.Len() != 0 {
		t.Errorf("the gap check fired on top of a real finding:\n%s", q.String())
	}
}

func hasPath(ms []KeyMismatch, path string) bool {
	for _, m := range ms {
		if m.Path == path {
			return true
		}
	}
	return false
}

func TestErrorLinesStopsAtTheUsageBanner(t *testing.T) {
	const out = `ERROR

  Failed to load config file '/etc/katzenpost.toml': open /etc/katzenpost.toml: permission denied.

  The Katzenpost server is a mixnet node.

  USAGE

    server [command] [--flags]

  FLAGS
    -f --config   Path to the server configuration file
`
	got := errorLines(out)
	if !strings.Contains(got, "permission denied") {
		t.Errorf("the error itself was dropped:\n%s", got)
	}
	if strings.Contains(got, "--flags") || strings.Contains(got, "-f --config") {
		t.Errorf("the usage banner was kept:\n%s", got)
	}
	if n := len(strings.Split(got, "\n")); n > 6 {
		t.Errorf("kept %d lines, want at most 6:\n%s", n, got)
	}
}

func TestAJailedConfigNamesKeysInsideItsJail(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "var", "lib", "auth")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "peer_id_pub_key.pem"),
		[]byte("-----BEGIN ED25519 PUBLIC KEY-----\nAAAA\n-----END ED25519 PUBLIC KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "etc", "auth.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "[Server]\n  DataDir = \"/var/lib/auth\"\n\n[[Authorities]]\n  IdentityPublicKeyPem = \"/var/lib/auth/peer_id_pub_key.pem\"\n"
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ScanKeysIn(state, cfg, func(p string) string { return filepath.Join(root, p) })
	if len(got.Blocked) != 0 {
		t.Errorf("a key inside the jail is reported missing: %v", got.Blocked)
	}
	if got.DataDir != state {
		t.Errorf("DataDir = %q, want the host path %q", got.DataDir, state)
	}
	if got.Scanned == 0 {
		t.Errorf("no key scanned")
	}
}

func ScanKeys(stateDir, configPath string) KeyReport {
	return ScanKeysIn(stateDir, configPath, nil)
}
