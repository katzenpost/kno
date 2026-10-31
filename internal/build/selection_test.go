package build

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/pkg/freebsd"
	"github.com/katzenpost/kno/internal/source"
	"github.com/katzenpost/kno/internal/track"
)

func selectionGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "core.hooksPath=/dev/null"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func selectionWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func selectionFixture(t *testing.T) (track.Env, string) {
	t.Helper()
	kat, hpqc, repo := t.TempDir(), t.TempDir(), t.TempDir()
	selectionGit(t, hpqc, "init", "-q", "-b", "main")
	selectionWrite(t, filepath.Join(hpqc, "go.mod"), "module github.com/katzenpost/hpqc\ngo 1.26.9\n")
	selectionGit(t, hpqc, "add", "-A")
	selectionGit(t, hpqc, "commit", "-qm", "hpqc source")
	selectionGit(t, hpqc, "tag", "v1.2.3")
	hsha := selectionGit(t, hpqc, "rev-parse", "HEAD")
	selectionGit(t, kat, "init", "-q", "-b", "main")
	selectionWrite(t, filepath.Join(kat, "go.mod"), "module github.com/katzenpost/katzenpost\ngo 1.26.9\nrequire github.com/katzenpost/hpqc v1.2.3\n")
	selectionWrite(t, filepath.Join(kat, "go.sum"), "github.com/katzenpost/hpqc v1.2.3 h1:fixture-checksum\n")
	selectionWrite(t, filepath.Join(kat, "debian/changelog"), "katzenpost (9.9.9) unstable; urgency=medium\n")
	selectionGit(t, kat, "add", "-A")
	selectionGit(t, kat, "commit", "-qm", "selected source")
	selectionGit(t, kat, "tag", "v9.9.9")
	selectionWrite(t, filepath.Join(repo, "thirdparty/pins.env"), "GO_VERSION=1.26.10\nKATZENPOST_REF=v9.9.9\nKATZENPOST_SHA="+selectionGit(t, kat, "rev-parse", "HEAD")+"\nHPQC_URL="+hpqc+"\nHPQC_REF=v1.2.3\nHPQC_SHA="+hsha+"\n")
	return track.Env{Repo: repo, Network: "fixture", KatzenpostURL: kat, StateDir: t.TempDir()}, kat
}

func TestSelectedMainKeepsAnImmutableCommitAndTheActualDependencyLocks(t *testing.T) {
	e, upstream := selectionFixture(t)
	pinsPath := filepath.Join(e.Repo, "thirdparty/pins.env")
	original, _ := os.ReadFile(pinsPath)
	selected, err := SelectKatzenpost(e, "", "main", track.Logger{Tool: "test", W: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	sha := selectionGit(t, upstream, "rev-parse", "HEAD")
	if selected.Source.Commit != sha || !selected.Tip || !strings.HasSuffix(selected.Tag, ".git"+sha) {
		t.Fatalf("main was not assigned its immutable package version: %+v", selected)
	}
	mod, _ := os.ReadFile(filepath.Join(upstream, "go.mod"))
	sum, _ := os.ReadFile(filepath.Join(upstream, "go.sum"))
	if selected.Inputs.GoModSHA256 != bytesSHA256(mod) || selected.Inputs.GoSumSHA256 != bytesSHA256(sum) || selected.Inputs.HpqcRef != "v1.2.3" || selected.Inputs.GoVersion != "1.26.10" {
		t.Fatalf("incorrect selected inputs: %+v", selected.Inputs)
	}
	selectionWrite(t, filepath.Join(upstream, "later"), "main moved\n")
	selectionGit(t, upstream, "add", "-A")
	selectionGit(t, upstream, "commit", "-qm", "main moved")
	work, patches := t.TempDir(), t.TempDir()
	if _, err := source.MaterializeSource(work, selected.Pins, patches); err != nil {
		t.Fatal(err)
	}
	if got := selectionGit(t, filepath.Join(work, "katzenpost"), "rev-parse", "HEAD"); got != sha {
		t.Fatalf("build followed moved main: got %s want %s", got, sha)
	}
	if body, _ := os.ReadFile(filepath.Join(work, "go.work")); strings.Contains(string(body), "replace") {
		t.Fatalf("selected modules bypassed their go.sum lock: %s", body)
	}
	if got, _ := os.ReadFile(pinsPath); string(got) != string(original) {
		t.Fatal("source selection rewrote shared pins")
	}
	if selected.Inputs.KatzenpostSha != sha {
		t.Fatal("recorded source drifted")
	}
}

func TestSelectedTagBuildsTheTaggedCodeRatherThanMain(t *testing.T) {
	e, upstream := selectionFixture(t)
	want := selectionGit(t, upstream, "rev-parse", "HEAD")
	selectionWrite(t, filepath.Join(upstream, "later"), "not tagged\n")
	selectionGit(t, upstream, "add", "-A")
	selectionGit(t, upstream, "commit", "-qm", "later")
	s, err := SelectKatzenpost(e, "v9.9.9", "", track.Logger{Tool: "test", W: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if s.Source.Commit != want || s.Tag != "v9.9.9" || s.Tip {
		t.Fatalf("tag selected wrong source: %+v", s)
	}
}

func TestSelectedMainPackageVersionAndChangelogAreDistinctAndReproducible(t *testing.T) {
	sha := strings.Repeat("a", 40)
	a, err := selectedTipTag("katzenpost (0.0.107) unstable; urgency=medium\n", sha, "1600000000")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := selectedTipTag("katzenpost (0.0.107) unstable; urgency=medium\n", sha, "1600000000")
	c, _ := selectedTipTag("katzenpost (0.0.107) unstable; urgency=medium\n", strings.Repeat("b", 40), "1600000000")
	if a != b || a == c || !strings.HasPrefix(a, "v0.0.107+git") {
		t.Fatalf("bad version identity: %s %s %s", a, b, c)
	}
	if freebsd.Version(strings.TrimPrefix(a, "v")) == freebsd.Version(strings.TrimPrefix(c, "v")) {
		t.Fatal("FreeBSD package versions lost the selected commit identity")
	}
	tree := t.TempDir()
	selectionWrite(t, filepath.Join(tree, "debian/changelog"), "katzenpost (0.0.107) unstable; urgency=medium\n")
	if err := selectedChangelog(tree, a, "1600000000"); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(filepath.Join(tree, "debian/changelog")); !strings.HasPrefix(string(body), "katzenpost ("+strings.TrimPrefix(a, "v")+")") {
		t.Fatalf("package still reports stale tagged version: %s", body)
	}
}

func TestSelectedInputManifestRecordsActualSourceAndLockDigests(t *testing.T) {
	e, _ := selectionFixture(t)
	s, err := SelectKatzenpost(e, "", "main", track.Logger{Tool: "test", W: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	pkg := filepath.Join(dir, "katzenpost_fixture_amd64.deb")
	selectionWrite(t, pkg, "actual package bytes\n")
	o := Options{Repo: e.Repo, Network: e.Network, Tag: s.Tag, Arches: []string{"amd64"}, Source: s, SkipKno: true}
	if err := recordAndVerify(o, []string{pkg}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	var recorded PackageSums
	if err := json.Unmarshal(body, &recorded); err != nil {
		t.Fatal(err)
	}
	if recorded.Inputs == nil || recorded.Inputs.KatzenpostSha != s.Source.Commit || recorded.Inputs.GoSumSHA256 != s.Inputs.GoSumSHA256 || recorded.Packages[0].Sha256 != bytesSHA256([]byte("actual package bytes\n")) {
		t.Fatalf("source provenance does not describe actual build inputs: %s", body)
	}
}

func TestSelectedSourceFailsClosedForUnavailableToolchainsAndSignaturePolicy(t *testing.T) {
	if err := selectedToolchain("go 1.99.0\n", "1.26.10"); err == nil {
		t.Fatal("unverified Go upgrade was accepted")
	}
	if err := selectedToolchain("go 1.26.9\ntoolchain go1.27.0\n", "1.26.10"); err == nil {
		t.Fatal("toolchain directive ignored")
	}
	e, _ := selectionFixture(t)
	e.RequireSignatures = true
	if _, err := SelectKatzenpost(e, "", "main", track.Logger{Tool: "test", W: io.Discard}); err == nil {
		t.Fatal("unsigned explicit selection bypassed signatures")
	}
	e.RequireSignatures, e.VerifyTag = false, true
	if _, err := SelectKatzenpost(e, "", "main", track.Logger{Tool: "test", W: io.Discard}); err == nil {
		t.Fatal("main bypassed signed-tag policy")
	}
}

func TestSelectedDependencyLocksCannotChangeDuringCompilation(t *testing.T) {
	dir := t.TempDir()
	selectionWrite(t, filepath.Join(dir, "go.mod"), "module selected\n")
	selectionWrite(t, filepath.Join(dir, "go.sum"), "locked\n")
	in := &BuildInputs{GoModSHA256: bytesSHA256([]byte("module selected\n")), GoSumSHA256: bytesSHA256([]byte("locked\n"))}
	if err := verifySelectedLocks(dir, in); err != nil {
		t.Fatal(err)
	}
	selectionWrite(t, filepath.Join(dir, "go.sum"), "changed by compilation\n")
	if err := verifySelectedLocks(dir, in); err == nil {
		t.Fatal("changed dependency lock was accepted")
	}
}

func TestSelectedBuildAcceptsPodmanAndDockerFullImageIDs(t *testing.T) {
	id := strings.Repeat("a", 64)
	for _, value := range []string{id, "sha256:" + id} {
		body, _ := json.Marshal([]map[string]string{{"Id": value}})
		if got, err := selectedBuilderImageID(body); err != nil || got != id {
			t.Fatalf("full image ID %q rejected: %q %v", value, got, err)
		}
	}
	for _, body := range []string{`[]`, `[{"Id":"short"}]`, `[{"Id":""}]`} {
		if _, err := selectedBuilderImageID([]byte(body)); err == nil {
			t.Fatalf("non-immutable image identity accepted: %s", body)
		}
	}
}

func TestSelectedToolPackageVersionsRetainTheEntireSourceIdentity(t *testing.T) {
	e, _ := selectionFixture(t)
	first := SelectedToolVersion(e.Repo, e.Network, "v9.9.9", track.SourceCheckout{Commit: strings.Repeat("a", 40)})
	second := SelectedToolVersion(e.Repo, e.Network, "v9.9.9", track.SourceCheckout{Commit: strings.Repeat("a", 39) + "b"})
	if !strings.Contains(first, strings.Repeat("a", 40)) || first == second || freebsd.Version(first) == freebsd.Version(second) {
		t.Fatalf("tool version lost source identity: %s %s", first, second)
	}
}
