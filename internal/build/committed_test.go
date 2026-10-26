package build

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/katzenpost/kno/internal/release"
)

func writePkg(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

func TestNoCommittedListIsItsOwnError(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(t.TempDir(), "pkgs")
	writePkg(t, dir, "katzenpost_0.0.104_amd64.deb", "bytes")

	err := VerifyAgainstCommitted(repo, "namenlos", "v0.0.104", dir, "/nonexistent")
	if err == nil {
		t.Fatal("a build with no committed list was accepted")
	}
	if !errors.Is(err, ErrNoCommittedSums) {
		t.Errorf("the caller cannot tell this apart from a mismatch: %v", err)
	}
}

func TestSelfBuildRefusesWhatDoesNotReproduce(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(t.TempDir(), "pkgs")
	writePkg(t, dir, "katzenpost_0.0.104_amd64.deb", "what this host built")

	relDir := CommittedDir(repo, "namenlos", "v0.0.104")
	if err := os.MkdirAll(relDir, 0o755); err != nil {
		t.Fatal(err)
	}
	other := sha256.Sum256([]byte("what the release records"))
	if err := os.WriteFile(filepath.Join(relDir, SumsName),
		[]byte(hex.EncodeToString(other[:])+"  katzenpost_0.0.104_amd64.deb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyAgainstCommitted(repo, "namenlos", "v0.0.104", dir,
		"/nonexistent"); err == nil {
		t.Fatal("a build that does not match the committed digests was accepted")
	}
}

func TestAnUnsignedCommittedListVerifies(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(t.TempDir(), "pkgs")
	sum := writePkg(t, dir, "katzenpost_0.0.104_amd64.deb", "the release")

	relDir := CommittedDir(repo, "namenlos", "v0.0.104")
	if err := os.MkdirAll(relDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(relDir, SumsName),
		[]byte(sum+"  katzenpost_0.0.104_amd64.deb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyAgainstCommitted(repo, "namenlos", "v0.0.104", dir,
		"/nonexistent"); err != nil {
		t.Fatalf("an unsigned committed list was refused: %v", err)
	}
}

func TestTheToolsOwnPackagesAreNotCheckedAgainstTheList(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(t.TempDir(), "pkgs")
	writePkg(t, dir, "kno_0.0.104-7_amd64.deb", "the tool")
	writePkg(t, dir, "kno-host-config_0.0.104-7_all.deb", "its config")

	writePkg(t, dir, "kno-host-config-mixy_0.0.104-7_amd64.deb", "mixy's config")

	relDir := CommittedDir(repo, "namenlos", "v0.0.104")
	if err := os.MkdirAll(relDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(relDir, SumsName), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyAgainstCommitted(repo, "namenlos", "v0.0.104", dir,
		"/nonexistent"); err != nil {
		t.Fatalf("the tool's own packages were checked against the list: %v", err)
	}
}

func TestRecordCommittedMergesAcrossArchitectures(t *testing.T) {
	repo := gitRepo(t)
	one := filepath.Join(t.TempDir(), "amd64")
	recordBuild(t, one, "v0.0.104", "198", "katzenpost_0.0.104-198_amd64.deb", "amd64 bytes")
	if _, err := RecordCommitted(repo, "namenlos", "v0.0.104", one); err != nil {
		t.Fatal(err)
	}
	two := filepath.Join(t.TempDir(), "ppc64el")
	recordBuild(t, two, "v0.0.104", "198", "katzenpost_0.0.104-198_ppc64el.deb", "ppc bytes")
	if _, err := RecordCommitted(repo, "namenlos", "v0.0.104", two); err != nil {
		t.Fatal(err)
	}
	got := Recorded(CommittedDir(repo, "namenlos", "v0.0.104"))
	if len(got) != 2 {
		t.Fatalf("recorded %d package(s), want both architectures: %+v", len(got), got)
	}
}

func TestRecordCommittedRefusesAConflictingDigest(t *testing.T) {
	repo := gitRepo(t)
	one := filepath.Join(t.TempDir(), "first")
	recordBuild(t, one, "v0.0.104", "198", "katzenpost_0.0.104-198_amd64.deb", "bytes")
	if _, err := RecordCommitted(repo, "namenlos", "v0.0.104", one); err != nil {
		t.Fatal(err)
	}
	two := filepath.Join(t.TempDir(), "second")
	recordBuild(t, two, "v0.0.104", "198", "katzenpost_0.0.104-198_amd64.deb", "other bytes")
	if _, err := RecordCommitted(repo, "namenlos", "v0.0.104", two); err == nil {
		t.Fatal("a conflicting digest overwrote the record")
	}
}

func TestRecordingTheSameBuildTwiceIsANoOp(t *testing.T) {
	repo := gitRepo(t)
	dir := filepath.Join(t.TempDir(), "pkgs")
	recordBuild(t, dir, "v0.0.104", "198", "katzenpost_0.0.104-198_amd64.deb", "bytes")
	if _, err := RecordCommitted(repo, "namenlos", "v0.0.104", dir); err != nil {
		t.Fatal(err)
	}
	wrote, err := RecordCommitted(repo, "namenlos", "v0.0.104", dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(wrote) != 0 {
		t.Errorf("rewrote the record for an unchanged build: %v", wrote)
	}
}

func TestRecordCommittedRefusesADirtyRevision(t *testing.T) {
	repo := gitRepo(t)
	dir := filepath.Join(t.TempDir(), "pkgs")
	recordBuild(t, dir, "v0.0.104", "198+dirty", "katzenpost_0.0.104-198_amd64.deb", "bytes")
	_, err := RecordCommitted(repo, "namenlos", "v0.0.104", dir)
	if err == nil {
		t.Fatal("a dirty build was recorded as the release")
	}
	if !strings.Contains(err.Error(), "dirty") {
		t.Errorf("the error does not say why: %v", err)
	}
}

func TestRecordCommittedRefusesADirtyToolRevision(t *testing.T) {
	repo := gitRepo(t)
	dir := filepath.Join(t.TempDir(), "pkgs")
	recordBuild(t, dir, "v0.0.107", "", "katzenpost_0.0.107_amd64.deb", "bytes")
	path := filepath.Join(dir, ManifestName)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b = bytes.Replace(b, []byte(`"tag": "v0.0.107",`), []byte(`"tag": "v0.0.107", "tool_revision": "1+dirty",`), 1)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordCommitted(repo, "namenlos", "v0.0.107", dir); err == nil || !strings.Contains(err.Error(), "1+dirty") {
		t.Fatalf("a build with tool revision 1+dirty was recorded: %v", err)
	}
}

func TestCommittedRevisionIsReadBack(t *testing.T) {
	repo := gitRepo(t)
	dir := filepath.Join(t.TempDir(), "pkgs")
	recordBuild(t, dir, "v0.0.104", "198", "katzenpost_0.0.104-198_amd64.deb", "bytes")
	if _, err := RecordCommitted(repo, "namenlos", "v0.0.104", dir); err != nil {
		t.Fatal(err)
	}
	if got := RecordedRevision(CommittedDir(repo, "namenlos", "v0.0.104"), "v0.0.104"); got != "198" {
		t.Errorf("recorded revision = %q, want 198", got)
	}
}

func TestCommittedSumsPath(t *testing.T) {
	sums, sig := CommittedSums("/repo", "namenlos", "v0.0.104")
	if want := "/repo/networks/namenlos/.generated/releases/v0.0.104/" + SumsName; sums != want {
		t.Errorf("sums = %q, want %q", sums, want)
	}
	if sig != sums+".sig" {
		t.Errorf("sig = %q, want %q", sig, sums+".sig")
	}
}

func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	c := exec.Command("git", "-C", repo, "init", "-q", "-b", "main")
	c.Env = append(os.Environ(), "GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0=/dev/null")
	if b, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, b)
	}
	return repo
}

func recordBuild(t *testing.T, dir, tag, revision, name, body string) {
	t.Helper()
	sum := writePkg(t, dir, name, body)
	m := PackageSums{
		Network:  "namenlos",
		Tag:      tag,
		Revision: revision,
		Written:  time.Now().UTC(),
		Packages: []PackageSum{{Name: name, Sha256: sum, Bytes: int64(len(body))}},
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestName), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestABuildMayProducePackagesTheRecordDoesNotNameYet(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(t.TempDir(), "pkgs")
	sum := writePkg(t, dir, "namenlos-config-mixy_0.0.104-21_amd64.deb", "mixy")
	writePkg(t, dir, "namenlos-config-reed_0.0.104-21_amd64.deb", "reed")

	relDir := CommittedDir(repo, "namenlos", "v0.0.104")
	if err := os.MkdirAll(relDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(relDir, SumsName),
		[]byte(sum+"  namenlos-config-mixy_0.0.104-21_amd64.deb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := DiffAgainstCommitted(repo, "namenlos", "v0.0.104", dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Mismatched) != 0 {
		t.Errorf("a package that reproduces was reported as a mismatch: %v", d.Mismatched)
	}
	if len(d.Unnamed) != 1 || d.Unnamed[0] != "namenlos-config-reed_0.0.104-21_amd64.deb" {
		t.Errorf("Unnamed = %v, want just reed's", d.Unnamed)
	}

	if err := VerifyAgainstCommitted(repo, "namenlos", "v0.0.104", dir, ""); err == nil {
		t.Error("an install accepted a package the record does not name")
	}
}

func TestAnExplicitRevisionBeatsTheRecordedOne(t *testing.T) {
	repo := t.TempDir()
	relDir := CommittedDir(repo, "namenlos", "v0.0.104")
	if err := os.MkdirAll(relDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(relDir, ManifestName),
		[]byte(`{"tag":"v0.0.104","tool_revision":"1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	o := Options{Repo: repo, Network: "namenlos", Tag: "v0.0.104"}
	if got := o.toolRevision(); got != "1" {
		t.Errorf("an ordinary build must take the recorded 1, got %q", got)
	}
	o.ToolRevision = "263"
	if got := o.toolRevision(); got != "263" {
		t.Errorf("--revision was overridden by the record: %q", got)
	}
}

func TestAnExplicitRevisionDefeatsContentStableReuse(t *testing.T) {
	id := ID("namenlos-config-mixy-pq-mixserver_0.0.104-1_amd64.deb")
	recorded := []PackageSum{{
		Name:    "namenlos-config-mixy-pq-mixserver_0.0.104-1_amd64.deb",
		Version: "0.0.104-1",
		Content: "samebytes",
	}}

	ordinary := NewStablePreferring(recorded, nil)
	if got, _ := ordinary.Version(id, "samebytes", "0.0.104-263"); got != "0.0.104-1" {
		t.Errorf("an ordinary build renumbered unchanged content: %q", got)
	}

	var explicit *Stable
	if got, _ := explicit.Version(id, "samebytes", "0.0.104-263"); got != "0.0.104-263" {
		t.Errorf("a named revision was overridden: %q", got)
	}
}

func TestRecordPruningDropsAPackageThisBuildNoLongerMakes(t *testing.T) {
	repo := gitRepo(t)
	both := filepath.Join(t.TempDir(), "both")
	recordBuild(t, both, "v0.0.104", "198",
		"namenlos-config-mine_0.0.104-198_amd64.deb", "mine")
	if _, err := RecordCommitted(repo, "namenlos", "v0.0.104", both); err != nil {
		t.Fatal(err)
	}
	theirs := filepath.Join(t.TempDir(), "theirs")
	recordBuild(t, theirs, "v0.0.104", "198",
		"namenlos-config-theirs_0.0.104-198_arm64.deb", "theirs")
	if _, err := RecordCommitted(repo, "namenlos", "v0.0.104", theirs); err != nil {
		t.Fatal(err)
	}
	if got := Recorded(CommittedDir(repo, "namenlos", "v0.0.104")); len(got) != 2 {
		t.Fatalf("recorded %d, want the merge of both builds: %+v", len(got), got)
	}
	if _, err := RecordPruning(repo, "namenlos", "v0.0.104", both); err != nil {
		t.Fatal(err)
	}
	got := Recorded(CommittedDir(repo, "namenlos", "v0.0.104"))
	if len(got) != 1 || got[0].Name != "namenlos-config-mine_0.0.104-198_amd64.deb" {
		t.Fatalf("prune left %+v, want only the package this build made", got)
	}
}

func TestRecordCommittedStillMergesAndNeverPrunes(t *testing.T) {
	repo := gitRepo(t)
	one := filepath.Join(t.TempDir(), "one")
	recordBuild(t, one, "v0.0.104", "198", "katzenpost_0.0.104-198_amd64.deb", "a")
	if _, err := RecordCommitted(repo, "namenlos", "v0.0.104", one); err != nil {
		t.Fatal(err)
	}
	two := filepath.Join(t.TempDir(), "two")
	recordBuild(t, two, "v0.0.104", "198", "katzenpost_0.0.104-198_arm64.deb", "b")
	if _, err := RecordCommitted(repo, "namenlos", "v0.0.104", two); err != nil {
		t.Fatal(err)
	}
	if got := Recorded(CommittedDir(repo, "namenlos", "v0.0.104")); len(got) != 2 {
		t.Fatalf("RecordCommitted dropped a name: %+v", got)
	}
}

func TestTheFreeBSDKnoPackagesAreSelfPackages(t *testing.T) {
	for name, want := range map[string]bool{
		"kno_0.0.107-1+2026_amd64.deb":                           true,
		"kno-host-config-reed_0.0.107-1_amd64.deb":               true,
		"kno-0.0.107.20261019102301.g8522455_1.pkg":              true,
		"kno-host-config-pietje-0.0.107.20261019102301.g1_1.pkg": true,
		"katzenpost-0.0.107.pkg":                                 false,
		"namenlos-config-kpclientd-0.0.107.pkg":                  false,
		"katzenpost_0.0.107+debian13_amd64.deb":                  false,
	} {
		if got := selfPackage(name); got != want {
			t.Errorf("selfPackage(%s) = %v, want %v: kno and its host config embed the repository that holds the record", name, got, want)
		}
	}
}

func TestTheRecordChecksFreeBSDPackagesToo(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(t.TempDir(), "pkgs")
	sum := writePkg(t, dir, "katzenpost-0.0.107.pkg", "recorded")
	writePkg(t, dir, "namenlos-config-allmixedup-pq-gatewaynode-0.0.107.20261020.gabc.pkg", "built")
	writePkg(t, dir, "namenlos-config-respectmy-pq-authority-0.0.107.20261020.gabc.pkg", "new")
	writePkg(t, dir, "kno-0.0.107.20261020.gabc_1.pkg", "kno")

	relDir := CommittedDir(repo, "namenlos", "v0.0.107")
	if err := os.MkdirAll(relDir, 0o755); err != nil {
		t.Fatal(err)
	}
	list := sum + "  katzenpost-0.0.107.pkg\n" +
		strings.Repeat("0", 64) + "  namenlos-config-allmixedup-pq-gatewaynode-0.0.107.20261020.gabc.pkg\n"
	if err := os.WriteFile(filepath.Join(relDir, SumsName), []byte(list), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := DiffAgainstCommitted(repo, "namenlos", "v0.0.107", dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Mismatched) != 1 || !strings.HasPrefix(d.Mismatched[0], "namenlos-config-allmixedup-") {
		t.Errorf("Mismatched = %v, want the allmixedup package", d.Mismatched)
	}
	if len(d.Unnamed) != 1 || !strings.HasPrefix(d.Unnamed[0], "namenlos-config-respectmy-") {
		t.Errorf("Unnamed = %v, want the respectmy package", d.Unnamed)
	}
	if err := VerifyAgainstCommitted(repo, "namenlos", "v0.0.107", dir, ""); err == nil {
		t.Error("a FreeBSD host accepted packages the record does not describe")
	}
}

func TestATarballThatDoesNotReproduceIsRefused(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(t.TempDir(), "pkgs")
	name := "katzenpost-0.0.107-debian13-amd64.tar.gz"
	writePkg(t, dir, name, "what this host built")
	writePkg(t, dir, "kno-0.0.107-src.tar.gz", "the kno source at its tag")

	relDir := CommittedDir(repo, "namenlos", "v0.0.107")
	if err := os.MkdirAll(relDir, 0o755); err != nil {
		t.Fatal(err)
	}
	other := sha256.Sum256([]byte("what the release records"))
	if err := os.WriteFile(filepath.Join(relDir, SumsName),
		[]byte(hex.EncodeToString(other[:])+"  "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := DiffAgainstCommitted(repo, "namenlos", "v0.0.107", dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Mismatched) != 1 || !strings.HasPrefix(d.Mismatched[0], name) {
		t.Errorf("mismatched %v, want %s: a tarball the record names must match it", d.Mismatched, name)
	}
	if len(d.Unnamed) != 0 {
		t.Errorf("unnamed %v: the kno source tarball embeds this repository, so no record names it", d.Unnamed)
	}
}

func TestRecordRefusesAPackageNameWithTheDirtyMark(t *testing.T) {
	for _, p := range []PackageSum{
		{Name: "namenlos-config-mixy-pq-mixserver_0.0.107-1+20261021100000.gabc1234.dirty_amd64.deb"},
		{Name: "namenlos-config-mixy-pq-mixserver-0.0.107.20261021100000.gabc1234.dirty.pkg"},
		{Name: "namenlos-config-mixy-pq-mixserver_x_amd64.deb", Version: "0.0.107-1+20261021100000.gabc1234.dirty"},
	} {
		repo := gitRepo(t)
		dir := filepath.Join(t.TempDir(), "pkgs")
		kp := writePkg(t, dir, "katzenpost_0.0.107+debian13_amd64.deb", "binary")
		p.Sha256 = writePkg(t, dir, p.Name, "config")
		m := PackageSums{Network: "namenlos", Tag: "v0.0.107", Revision: "1", Written: time.Now().UTC(),
			Packages: []PackageSum{{Name: "katzenpost_0.0.107+debian13_amd64.deb", Sha256: kp}, p}}
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ManifestName), b, 0o644); err != nil {
			t.Fatal(err)
		}
		wrote, err := RecordCommitted(repo, "namenlos", "v0.0.107", dir)
		if err == nil || !strings.Contains(err.Error(), p.Name) {
			t.Errorf("%s %s: recorded %v, err %v; want a refusal that names it", p.Name, p.Version, wrote, err)
		}
		if _, err := os.Stat(CommittedDir(repo, "namenlos", "v0.0.107")); err == nil {
			t.Errorf("%s: the refusal still wrote a record", p.Name)
		}
	}
}

func TestRecordKeepsTheToolsDirtyPackagesOut(t *testing.T) {
	repo := gitRepo(t)
	dir := filepath.Join(t.TempDir(), "pkgs")
	kp := writePkg(t, dir, "katzenpost_0.0.107+debian13_amd64.deb", "binary")
	kno := writePkg(t, dir, "kno_0.0.107-1+20261021100000.gabc1234.dirty_amd64.deb", "tool")
	m := PackageSums{Network: "namenlos", Tag: "v0.0.107", Revision: "1", Written: time.Now().UTC(),
		Packages: []PackageSum{
			{Name: "katzenpost_0.0.107+debian13_amd64.deb", Sha256: kp},
			{Name: "kno_0.0.107-1+20261021100000.gabc1234.dirty_amd64.deb", Sha256: kno},
		}}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestName), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordCommitted(repo, "namenlos", "v0.0.107", dir); err != nil {
		t.Fatalf("a dirty kno package blocked the record, but the record never names it: %v", err)
	}
}

func CommittedSums(repo, network, tag string) (sums, sig string) {
	return release.Sums(repo, network, tag)
}
