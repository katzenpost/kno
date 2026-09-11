package activate

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/katzenpost/kno/internal/sign"
	"github.com/katzenpost/kno/internal/testutil"
	"github.com/katzenpost/kno/internal/track"
)

func relWithPkgs(t *testing.T, sums bool) string {
	t.Helper()
	rel := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rel, "pkgs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rel, "pkgs", "katzenpost_0.0.104_amd64.deb"),
		[]byte("deb"), 0o644); err != nil {
		t.Fatal(err)
	}
	if sums {
		if err := os.WriteFile(filepath.Join(rel, "pkgs", "SHA256SUMS"),
			[]byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return rel
}

func allowlistAt(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "allowed_signers")
	if err := os.WriteFile(p, []byte("op ssh-ed25519 AAAA\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAReleaseWithNoDigestListIsRefused(t *testing.T) {
	for _, allow := range []string{allowlistAt(t), filepath.Join(t.TempDir(), "absent")} {
		o := Options{
			Rel:       relWithPkgs(t, false),
			Tag:       "v0.0.104",
			Backend:   "systemd",
			Allowlist: allow,
		}
		if rc := checkRelease(track.Logger{Tool: "activate", W: io.Discard}, o); rc == 0 {
			t.Errorf("a release with no SHA256SUMS was accepted (allowlist %s)", allow)
		}
	}
}

func TestAnUnsignedReleaseIsInstalledOnItsDigests(t *testing.T) {
	o := Options{
		Rel:       relWithPkgs(t, true),
		Tag:       "v0.0.104",
		Backend:   "systemd",
		Allowlist: allowlistAt(t),
	}
	if rc := checkRelease(track.Logger{Tool: "activate", W: io.Discard}, o); rc != 0 {
		t.Errorf("an unsigned release was refused, got rc=%d", rc)
	}
}

func TestAReleaseSignedByAKeyThisHostRejectsIsRefused(t *testing.T) {
	if !sign.Available() {
		t.Skip("no ssh-keygen to verify with")
	}
	rel := relWithPkgs(t, true)
	if err := os.WriteFile(filepath.Join(rel, "pkgs", "SHA256SUMS.sig"),
		[]byte("-----BEGIN SSH SIGNATURE-----\nnot a signature\n-----END SSH SIGNATURE-----\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	o := Options{
		Rel:       rel,
		Tag:       "v0.0.104",
		Backend:   "systemd",
		Allowlist: allowlistAt(t),
	}
	if rc := checkRelease(track.Logger{Tool: "activate", W: io.Discard}, o); rc == 0 {
		t.Error("a release with an unverifiable signature was accepted")
	}
}

func TestLooseBinaryBackendIsNotCaughtByTheSumsRefusal(t *testing.T) {

	repo := testutil.RepoRoot(t)
	real := Options{
		Rel:      relWithPkgs(t, false),
		Tag:      "v0.0.104",
		Repo:     repo,
		Network:  "namenlos",
		Services: []string{"wauland-pq-mixserver"},
	}
	if got := hostBackend(real); got != "docker" {
		t.Fatalf("wauland's backend resolved to %q, want docker", got)
	}
	if rc := checkRelease(track.Logger{Tool: "activate", W: io.Discard}, real); rc != 0 {
		t.Errorf("a docker host was refused for staging no sums, got rc=%d", rc)
	}

	o := Options{
		Rel:       relWithPkgs(t, false),
		Tag:       "v0.0.104",
		Backend:   "freebsd-ezjail",
		Allowlist: allowlistAt(t),
	}
	if rc := checkRelease(track.Logger{Tool: "activate", W: io.Discard}, o); rc == 0 {
		t.Errorf("an ezjail host installs packages, so a release with no sums must be refused")
	}
}

func TestASubstitutedPackageIsRefused(t *testing.T) {
	rel := t.TempDir()
	pkgs := filepath.Join(rel, "pkgs")
	if err := os.MkdirAll(pkgs, 0o755); err != nil {
		t.Fatal(err)
	}
	deb := filepath.Join(pkgs, "katzenpost_0.0.104_amd64.deb")
	if err := os.WriteFile(deb, []byte("the real package"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("the real package"))
	if err := os.WriteFile(filepath.Join(pkgs, "SHA256SUMS"),
		[]byte(hex.EncodeToString(sum[:])+"  katzenpost_0.0.104_amd64.deb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := track.Logger{Tool: "activate", W: io.Discard}
	o := Options{Rel: rel, Tag: "v0.0.104"}

	if rc := verifyPackageDigests(l, o, []string{deb}); rc != 0 {
		t.Fatalf("the package the list names must pass, got rc=%d", rc)
	}
	if err := os.WriteFile(deb, []byte("a trojaned package"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rc := verifyPackageDigests(l, o, []string{deb}); rc == 0 {
		t.Error("a package whose bytes differ from the recorded digest was accepted")
	}
}

func TestAPackageNotNamedInTheDigestListIsRefused(t *testing.T) {
	rel := t.TempDir()
	pkgs := filepath.Join(rel, "pkgs")
	if err := os.MkdirAll(pkgs, 0o755); err != nil {
		t.Fatal(err)
	}
	deb := filepath.Join(pkgs, "sneaked_0.0.104_amd64.deb")
	if err := os.WriteFile(deb, []byte("unlisted"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgs, "SHA256SUMS"),
		[]byte("deadbeef  katzenpost_0.0.104_amd64.deb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := track.Logger{Tool: "activate", W: io.Discard}
	if rc := verifyPackageDigests(l, Options{Rel: rel, Tag: "v0.0.104"}, []string{deb}); rc == 0 {
		t.Error("a package absent from the digest list was accepted")
	}
}

func TestAThresholdShortfallWarnsRatherThanRefuses(t *testing.T) {
	o := Options{
		Rel:       relWithPkgs(t, true),
		Tag:       "v0.0.104",
		Backend:   "systemd",
		Allowlist: allowlistAt(t),
		Threshold: 2,
	}
	if rc := checkRelease(track.Logger{Tool: "activate", W: io.Discard}, o); rc != 0 {
		t.Errorf("a threshold shortfall refused the install, got rc=%d", rc)
	}
}

func TestValidSignersDeduplicatesAPrincipal(t *testing.T) {
	who, _ := validSigners(filepath.Join(t.TempDir(), "SHA256SUMS"), "/nonexistent")
	if len(who) != 0 {
		t.Errorf("no signatures present but got %v", who)
	}
}

func TestAStagedManifestCannotChooseTheBackend(t *testing.T) {
	repo := testutil.RepoRoot(t)
	rel := relWithPkgs(t, false)
	if err := os.MkdirAll(filepath.Join(rel, "manifest"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rel, "manifest", "mixy-pq-mixserver.conf"),
		[]byte("BACKEND=\"docker\"\nDOCKER_IMAGE=\"katzenpost/mix\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := ReleaseBackend(rel); got != "docker" {
		t.Fatalf("the fixture does not claim docker: %q", got)
	}

	o := Options{
		Rel:      rel,
		Tag:      "v0.0.104",
		Repo:     repo,
		Network:  "namenlos",
		Services: []string{"mixy-pq-mixserver"},
	}
	if got := hostBackend(o); got != "systemd" {
		t.Fatalf("hostBackend = %q, want the repository's systemd", got)
	}
	if rc := checkRelease(track.Logger{Tool: "activate", W: io.Discard}, o); rc == 0 {
		t.Error("a staged manifest claiming docker skipped the digest requirement")
	}
}

func TestTheCallersDirectoryCannotChooseTheBackend(t *testing.T) {

	fake := t.TempDir()
	svcDir := filepath.Join(fake, "networks", "namenlos", "editable-configs", "services")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svcDir, "mixy-pq-mixserver.conf"),
		[]byte("BACKEND=docker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := Options{
		Rel:      relWithPkgs(t, false),
		Tag:      "v0.0.105",
		Repo:     fake,
		Network:  "namenlos",
		Services: []string{"mixy-pq-mixserver"},
	}

	if got := hostBackend(o); got != "systemd" {
		t.Errorf("hostBackend = %q from a caller-chosen repo, want systemd", got)
	}
	if rc := checkRelease(track.Logger{Tool: "activate", W: io.Discard}, o); rc == 0 {
		t.Error("a caller-chosen repo turned off the digest requirement")
	}
}

func TestWithNoEmbeddedDataTheBackendIsUnknownAndTheListIsRequired(t *testing.T) {

	t.Setenv("KNO_CACHE", filepath.Join(os.DevNull, "nope"))
	fake := t.TempDir()
	svcDir := filepath.Join(fake, "networks", "namenlos", "editable-configs", "services")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svcDir, "mixy-pq-mixserver.conf"),
		[]byte("BACKEND=docker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := Options{
		Rel:      relWithPkgs(t, false),
		Tag:      "v0.0.105",
		Repo:     fake,
		Network:  "namenlos",
		Services: []string{"mixy-pq-mixserver"},
	}
	if got := hostBackend(o); got != "" {
		t.Errorf("hostBackend = %q with no embedded data, want unknown", got)
	}
	if rc := checkRelease(track.Logger{Tool: "activate", W: io.Discard}, o); rc == 0 {
		t.Error("an unreadable cache turned off the digest requirement")
	}
}
