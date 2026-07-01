package build

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/katzenpost/kno/internal/testutil"
)

func Sums(network, tag string, pkgs []string, now time.Time) (PackageSums, error) {
	return SumsWithContent(network, tag, "", "", pkgs, nil, now)
}

func stagePkgs(t *testing.T, dir string, files map[string]string) []string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var out []string
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func TestSha256sumCanVerifyTheFile(t *testing.T) {
	if _, err := exec.LookPath("sha256sum"); err != nil {
		t.Skip("sha256sum not available")
	}
	dir := t.TempDir()
	pkgs := stagePkgs(t, dir, map[string]string{
		"katzenpost_0.0.104_amd64.deb":                      "one",
		"namenlos-config-mixy-pq-mixserver_0.0.104_all.deb": "two",
	})
	sums, err := Sums("namenlos", "v0.0.104", pkgs, time.Unix(1600000000, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteSums(dir, sums); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("sha256sum", "-c", SumsName)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sha256sum -c failed: %v\n%s", err, out)
	}

	if n := strings.Count(string(out), ": OK"); n != 3 {
		t.Errorf("verified %d, want 2 packages and the manifest:\n%s", n, out)
	}
	if !strings.Contains(string(out), ManifestName+": OK") {
		t.Errorf("%s is not covered by %s:\n%s", ManifestName, SumsName, out)
	}

	if err := os.WriteFile(filepath.Join(dir, "katzenpost_0.0.104_amd64.deb"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("sha256sum", "-c", SumsName)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Errorf("a modified package still verified:\n%s", out)
	}
}

func TestSumsFileUsesBasenames(t *testing.T) {
	dir := t.TempDir()
	pkgs := stagePkgs(t, dir, map[string]string{"kno_0.0.104-1_amd64.deb": "x"})
	sums, err := Sums("namenlos", "v0.0.104", pkgs, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	body := sums.SumsFile()
	if strings.Contains(body, dir) {
		t.Errorf("the sums file carries an absolute path:\n%s", body)
	}
	if !strings.Contains(body, "  kno_0.0.104-1_amd64.deb\n") {
		t.Errorf("not coreutils format (hash, two spaces, name):\n%q", body)
	}
}

func TestSumsAreSortedByName(t *testing.T) {
	dir := t.TempDir()
	pkgs := stagePkgs(t, dir, map[string]string{
		"zzz_0.0.104_all.deb": "z", "aaa_0.0.104_all.deb": "a", "mmm_0.0.104_all.deb": "m",
	})
	sums, err := Sums("namenlos", "v0.0.104", pkgs, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range sums.Packages {
		names = append(names, p.Name)
	}
	if strings.Join(names, " ") != "aaa_0.0.104_all.deb mmm_0.0.104_all.deb zzz_0.0.104_all.deb" {
		t.Errorf("not sorted: %v", names)
	}
}

func TestManifestCarriesTheBuildIdentity(t *testing.T) {
	dir := t.TempDir()
	pkgs := stagePkgs(t, dir, map[string]string{"kno_0.0.104-1_amd64.deb": "x"})
	sums, err := Sums("namenlos", "v0.0.104", pkgs, time.Unix(1600000000, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteSums(dir, sums); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	var got PackageSums
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("the manifest is not valid JSON: %v", err)
	}
	if got.Network != "namenlos" || got.Tag != "v0.0.104" {
		t.Errorf("manifest does not identify the build: %+v", got)
	}
	if len(got.Packages) != 1 || got.Packages[0].Bytes != 1 {
		t.Errorf("manifest packages wrong: %+v", got.Packages)
	}
}

func TestVerifyCatchesAPackageThatChangedAfterHashing(t *testing.T) {
	dir := t.TempDir()
	pkgs := stagePkgs(t, dir, map[string]string{
		"katzenpost_0.0.104_amd64.deb": "the real bytes",
		"kno_0.0.104-1_amd64.deb":      "also real",
	})
	sums, err := Sums("namenlos", "v0.0.104", pkgs, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if bad := VerifySums(dir, sums); len(bad) != 0 {
		t.Fatalf("a freshly hashed build did not verify: %v", bad)
	}

	if err := os.WriteFile(filepath.Join(dir, "katzenpost_0.0.104_amd64.deb"), []byte("trunc"), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := VerifySums(dir, sums)
	if len(bad) != 1 {
		t.Fatalf("got %d failures, want 1: %v", len(bad), bad)
	}
	if bad[0].Name != "katzenpost_0.0.104_amd64.deb" {
		t.Errorf("named the wrong package: %s", bad[0].Name)
	}
	if !strings.Contains(bad[0].Error(), "recorded") {
		t.Errorf("the error does not say what was expected: %v", bad[0])
	}
}

func TestVerifyCatchesAMissingPackage(t *testing.T) {
	dir := t.TempDir()
	pkgs := stagePkgs(t, dir, map[string]string{"kno_0.0.104-1_amd64.deb": "x"})
	sums, err := Sums("namenlos", "v0.0.104", pkgs, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(pkgs[0]); err != nil {
		t.Fatal(err)
	}
	bad := VerifySums(dir, sums)
	if len(bad) != 1 || bad[0].Err == nil {
		t.Fatalf("a missing package was not caught: %v", bad)
	}
}

func TestVerifyReportIsQuietWhenCleanAndLoudWhenNot(t *testing.T) {
	sums := PackageSums{Packages: []PackageSum{{Name: "a.deb"}, {Name: "b.deb"}}}

	var quiet strings.Builder
	ReportVerify(&quiet, sums, nil, "/d", false, nil)
	if strings.Contains(quiet.String(), "a.deb") {
		t.Errorf("a clean non-verbose check listed every package:\n%s", quiet.String())
	}
	if quiet.String() != "" {
		t.Errorf("a clean check without -v printed:\n%s", quiet.String())
	}

	verbose = true
	defer func() { verbose = false }()
	var loud strings.Builder
	ReportVerify(&loud, sums, nil, "/d", true, nil)
	if !strings.Contains(loud.String(), "a.deb: OK") {
		t.Errorf("verbose does not list packages:\n%s", loud.String())
	}

	var failed strings.Builder
	ReportVerify(&failed, sums, []SumsError{{Name: "a.deb", Want: "aaa", Got: "bbb"}}, "/d", false, nil)
	for _, want := range []string{"do NOT match", "FAILED", "a.deb", "aaa", "bbb"} {
		if !strings.Contains(failed.String(), want) {
			t.Errorf("a failure is missing %q:\n%s", want, failed.String())
		}
	}
}

func TestASecondBuildKeepsTheFirstBuildsPackages(t *testing.T) {
	dir := t.TempDir()
	first := stagePkgs(t, dir, map[string]string{
		"namenlos-config-annares-pq-authority_0.0.104_all.deb": "mine",
	})
	a, err := Sums("namenlos", "v0.0.104", first, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteSums(dir, a); err != nil {
		t.Fatal(err)
	}

	second := stagePkgs(t, dir, map[string]string{
		"namenlos-config-oneeyedman-pq-mixserver_0.0.104_all.deb": "theirs",
	})
	b, err := Sums("namenlos", "v0.0.104", second, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	b = b.Merge(Existing(dir))
	if len(b.Packages) != 2 {
		t.Fatalf("the second build covers %d package(s), want both: %+v", len(b.Packages), b.Packages)
	}
	if err := WriteSums(dir, b); err != nil {
		t.Fatal(err)
	}
	if bad := VerifySums(dir, b); len(bad) != 0 {
		t.Fatalf("the merged sums do not verify: %v", bad)
	}

	if err := os.WriteFile(first[0], []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if bad := VerifySums(dir, b); len(bad) != 1 {
		t.Fatalf("corrupting the earlier build's package was not caught: %v", bad)
	}
}

func TestMergeDropsEntriesWhosePackageIsGone(t *testing.T) {
	dir := t.TempDir()
	p := stagePkgs(t, dir, map[string]string{"gone.deb": "x"})
	s, err := Sums("namenlos", "v0.0.104", p, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteSums(dir, s); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p[0]); err != nil {
		t.Fatal(err)
	}
	if got := Existing(dir); len(got) != 0 {
		t.Fatalf("a removed package is still recorded: %+v", got)
	}
}

func TestManifestTimeIsPinnedToSourceDateEpoch(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1700000000")
	if got := buildTime(); got.Unix() != 1700000000 {
		t.Errorf("buildTime() = %v, want the pinned epoch", got)
	}
	t.Setenv("SOURCE_DATE_EPOCH", "not-a-number")
	if buildTime().IsZero() {
		t.Error("an unparseable epoch left no timestamp at all")
	}
}

func TestVerboseListsWhatThisBuildMade(t *testing.T) {
	sums := PackageSums{Packages: []PackageSum{
		{Name: "mine-a.deb", Sha256: "aaa"},
		{Name: "theirs-b.deb", Sha256: "bbb"},
		{Name: "theirs-c.deb", Sha256: "ccc"},
	}}
	var out bytes.Buffer
	ReportVerify(&out, sums, nil, "/d", true, []string{"mine-a.deb"})
	got := out.String()
	if !strings.Contains(got, "mine-a.deb: OK") {
		t.Errorf("the package this build made is not listed:\n%s", got)
	}
	for _, n := range []string{"theirs-b.deb: OK", "theirs-c.deb: OK"} {
		if strings.Contains(got, n) {
			t.Errorf("listed %s, which this build did not produce:\n%s", n, got)
		}
	}

	if !strings.Contains(got, "and 2 package(s) already in this release") {
		t.Errorf("does not account for the rest of the release:\n%s", got)
	}
	if !strings.Contains(got, "3 packages match") {
		t.Errorf("the verified count is not the whole release:\n%s", got)
	}
}

func TestManifestRecordsWhatTheBuildWasMadeFrom(t *testing.T) {
	repo := testutil.RepoRoot(t)
	dir := t.TempDir()
	deb := filepath.Join(dir, "example_1.0_all.deb")
	if err := os.WriteFile(deb, []byte("not really a deb"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := SumsWithContent("namenlos", "v0.0.104", "198", repo,
		[]string{deb}, nil, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if s.Revision != "198" {
		t.Errorf("revision = %q, want 198", s.Revision)
	}
	if s.Inputs == nil {
		t.Fatal("no build inputs recorded")
	}
	for name, got := range map[string]string{
		"katzenpost ref": s.Inputs.KatzenpostRef,
		"katzenpost sha": s.Inputs.KatzenpostSha,
		"hpqc ref":       s.Inputs.HpqcRef,
		"go version":     s.Inputs.GoVersion,
		"build image":    s.Inputs.BuildImage,
		"tool":           s.Inputs.Tool,
	} {
		if got == "" {
			t.Errorf("%s was not recorded", name)
		}
	}

	if !strings.Contains(s.Inputs.BuildImage, "@sha256:") {
		t.Errorf("build image is not pinned by digest: %q", s.Inputs.BuildImage)
	}
}

func TestTheManifestIsTheAuthorityForTheRevision(t *testing.T) {
	dir := t.TempDir()
	write := func(m PackageSums) {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ManifestName), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write(PackageSums{Tag: "v0.0.104", Revision: "198"})
	none := filepath.Join(t.TempDir(), "uncommitted")
	got, why := revisionFor("", dir, none, "v0.0.104")
	if got != "198" {
		t.Errorf("revision = %q, want the recorded 198", got)
	}
	if !strings.Contains(why, ManifestName) {
		t.Errorf("it does not say where the revision came from: %q", why)
	}

	if got, _ := revisionFor("200", dir, none, "v0.0.104"); got != "200" {
		t.Errorf("--revision was overridden by the manifest: %q", got)
	}

	if got, _ := revisionFor("", dir, none, "v0.0.105"); got != "" {
		t.Errorf("took another tag's revision: %q", got)
	}

	write(PackageSums{Tag: "v0.0.104", Revision: "198+dirty"})
	if got, _ := revisionFor("", dir, none, "v0.0.104"); got != "" {
		t.Errorf("reused a dirty revision: %q", got)
	}

	committed := t.TempDir()
	b, err := json.Marshal(PackageSums{Tag: "v0.0.104", Revision: "198"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(committed, ManifestName), b, 0o644); err != nil {
		t.Fatal(err)
	}
	write(PackageSums{Tag: "v0.0.104", Revision: "204"})
	if got, _ := revisionFor("", dir, committed, "v0.0.104"); got != "198" {
		t.Errorf("revision = %q, want the committed 198", got)
	}
}

func TestDescribeNeedsNoDpkgOrXz(t *testing.T) {
	if _, err := exec.LookPath("dpkg-deb"); err != nil {
		t.Skip("no dpkg-deb to build and read the package with")
	}
	deb := realDeb(t)
	out, err := exec.Command("dpkg-deb", "-f", deb, "Architecture", "Version").Output()
	if err != nil {
		t.Fatal(err)
	}
	want := string(out)

	t.Setenv("PATH", "")
	arch, ver, scripts := describe(deb)
	if got := "Architecture: " + arch + "\nVersion: " + ver + "\n"; got != want {
		t.Errorf("without dpkg-deb and xz on PATH, describe read %q; dpkg-deb reads %q", got, want)
	}
	if strings.Join(scripts, ",") != "postinst" {
		t.Errorf("maintainer scripts = %v, want postinst", scripts)
	}
}

func TestPlaceUpstreamNeedsNoDpkgDeb(t *testing.T) {
	if _, err := exec.LookPath("dpkg-deb"); err != nil {
		t.Skip("no dpkg-deb to build the package with")
	}
	root := t.TempDir()
	pkg := filepath.Join(root, "pkg")
	for _, d := range []string{"DEBIAN", "usr/bin", "etc/katzenpost"} {
		if err := os.MkdirAll(filepath.Join(pkg, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for p, body := range map[string]string{
		"DEBIAN/control": "Package: katzenpost\nVersion: 0.0.105+debian13\nArchitecture: amd64\n" +
			"Maintainer: t <t@example.invalid>\nDescription: t\n",
		"usr/bin/kpclientd":              "client",
		"etc/katzenpost/thinclient.toml": "cfg",
	} {
		if err := os.WriteFile(filepath.Join(pkg, p), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	built := filepath.Join(root, "katzenpost_0.0.105+debian13_amd64.deb")
	if b, err := exec.Command("dpkg-deb", "-Zxz", "--root-owner-group", "--build", pkg, built).CombinedOutput(); err != nil {
		t.Skipf("could not build a test package: %v: %s", err, b)
	}

	t.Setenv("PATH", "")
	if v, err := debField(built, "Version"); err != nil || v != "0.0.105+debian13" {
		t.Errorf("debField = %q, %v; want 0.0.105+debian13", v, err)
	}
	bindir := filepath.Join(root, "bin-amd64-debian13")
	dst, err := placeUpstream(built, filepath.Join(root, "pkgs"), "v0.0.105", "debian-13", "amd64", bindir)
	if err != nil {
		t.Fatalf("placeUpstream without dpkg-deb: %v", err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("the deb was not placed: %v", err)
	}
	ents, err := os.ReadDir(bindir)
	if err != nil || len(ents) != 1 || ents[0].Name() != "kpclientd" {
		t.Errorf("bindir holds %v (%v), want kpclientd only", ents, err)
	}
}

func realDeb(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	ctrl := filepath.Join(root, "pkg", "DEBIAN")
	if err := os.MkdirAll(ctrl, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ctrl, "control"), []byte(
		"Package: kno-reader-test\nVersion: 1.2-3\nArchitecture: all\n"+
			"Maintainer: t <t@example.invalid>\nDescription: t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ctrl, "postinst"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "t.deb")

	cmd := exec.Command("dpkg-deb", "-Zxz", "--build", filepath.Join(root, "pkg"), out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("could not build a test package: %v: %s", err, b)
	}
	return out
}

func TestTamperingWithTheManifestBreaksTheSums(t *testing.T) {
	if _, err := exec.LookPath("sha256sum"); err != nil {
		t.Skip("no sha256sum")
	}
	dir := t.TempDir()
	pkgs := stagePkgs(t, dir, map[string]string{"katzenpost_1_amd64.deb": "one"})
	sums, err := Sums("namenlos", "v1", pkgs, time.Unix(1600000000, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteSums(dir, sums); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	altered := strings.Replace(string(b), `"namenlos"`, `"somewhere-else"`, 1)
	if altered == string(b) {
		t.Fatal("the manifest did not contain what this test edits")
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte(altered), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("sha256sum", "-c", SumsName)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("an edited manifest still verified:\n%s", out)
	}
	if !strings.Contains(string(out), ManifestName) {
		t.Errorf("the failure does not name the manifest:\n%s", out)
	}
}
