package build

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func pkg(name, digest, version string, scripts ...string) PackageSum {
	return PackageSum{Name: name, Sha256: digest, Version: version, Scripts: scripts}
}

func TestCompareCatchesADigestThatDoesNotReproduce(t *testing.T) {
	ref := PackageSums{Tag: "v0.0.104", Packages: []PackageSum{
		pkg("katzenpost_0.0.104-198_amd64.deb", "aaa", "0.0.104-198"),
	}}
	got := PackageSums{Tag: "v0.0.104", Packages: []PackageSum{
		pkg("katzenpost_0.0.104-198_amd64.deb", "bbb", "0.0.104-198"),
	}}
	diffs := Compare(ref, got)
	if len(diffs) != 1 || diffs[0].Kind != "digest" {
		t.Fatalf("want one digest difference, got %+v", diffs)
	}
	var out strings.Builder
	if rc := ReportCompare(&out, ref, got, diffs); rc == 0 {
		t.Error("a build that did not reproduce the release exited 0")
	}
	if !strings.Contains(out.String(), "did not reproduce") {
		t.Errorf("the report does not say what happened:\n%s", out.String())
	}
}

func TestCompareCatchesANewMaintainerScript(t *testing.T) {
	ref := PackageSums{Tag: "v1", Packages: []PackageSum{pkg("a_1_all.deb", "aaa", "1")}}
	got := PackageSums{Tag: "v1", Packages: []PackageSum{pkg("a_1_all.deb", "aaa", "1", "postinst")}}
	diffs := Compare(ref, got)
	if len(diffs) != 1 || diffs[0].Kind != "scripts" {
		t.Fatalf("want one scripts difference, got %+v", diffs)
	}
	if diffs[0].Want != "(none)" || diffs[0].Got != "postinst" {
		t.Errorf("the difference does not name the change: %+v", diffs[0])
	}
}

func TestCompareCatchesMissingAndExtraPackages(t *testing.T) {
	ref := PackageSums{Tag: "v1", Packages: []PackageSum{
		pkg("a_1_all.deb", "aaa", "1"), pkg("b_1_all.deb", "bbb", "1"),
	}}
	got := PackageSums{Tag: "v1", Packages: []PackageSum{
		pkg("a_1_all.deb", "aaa", "1"), pkg("c_1_all.deb", "ccc", "1"),
	}}
	kinds := map[string]string{}
	for _, d := range Compare(ref, got) {
		kinds[d.Package] = d.Kind
	}
	if kinds["b_1_all.deb"] != "missing" {
		t.Errorf("a package in the manifest and not in the build was not reported: %v", kinds)
	}
	if kinds["c_1_all.deb"] != "unexpected" {
		t.Errorf("a package in the build and not in the manifest was not reported: %v", kinds)
	}
}

func TestCompareRefusesTwoDifferentReleases(t *testing.T) {
	ref := PackageSums{Tag: "v0.0.104"}
	got := PackageSums{Tag: "v0.0.105"}
	var out strings.Builder
	if rc := ReportCompare(&out, ref, got, nil); rc == 0 {
		t.Error("comparing two different tags passed")
	}
	if !strings.Contains(out.String(), "would say nothing") {
		t.Errorf("it does not explain why: %s", out.String())
	}
}

func TestAnIdenticalBuildPasses(t *testing.T) {
	m := PackageSums{Tag: "v1", Packages: []PackageSum{
		pkg("a_1_all.deb", "aaa", "1", "postinst"),
	}}
	var out strings.Builder
	if rc := ReportCompare(&out, m, m, Compare(m, m)); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, out.String())
	}
	if !strings.Contains(out.String(), "1 package(s) match") {
		t.Errorf("it does not say what it checked:\n%s", out.String())
	}
}

func writeBuild(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	var paths []string
	for n, body := range files {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)
	s, err := Sums("namenlos", "v0.0.107", paths, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteSums(dir, s); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runCompare(t *testing.T, ref, dir string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd := compareCommand()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{ref, "--dir", dir})
	err := cmd.Execute()
	if err != nil {
		out.WriteString(err.Error())
	}
	return out.String(), err
}

const (
	kpAmd64     = "katzenpost_0.0.107+debian13_amd64.deb"
	kpPpc64el   = "katzenpost_0.0.107+ubuntu26.04_ppc64el.deb"
	knoAmd64    = "kno_0.0.107-1_amd64.deb"
	knoPpc64el  = "kno_0.0.107-1_ppc64el.deb"
	cfgMixy     = "namenlos-config-mixy-pq-mixserver_0.0.107-1_amd64.deb"
	cfgAnnares  = "namenlos-config-annares-pq-authority_0.0.107-1_ppc64el.deb"
	hostAnnares = "kno-host-config-annares_0.0.107-1_ppc64el.deb"
)

func TestCompareFailsWhenTheSecondBuildLacksTheKnoDebOfAnArch(t *testing.T) {
	first := writeBuild(t, map[string]string{kpPpc64el: "kp", knoPpc64el: "kno"})
	second := writeBuild(t, map[string]string{kpPpc64el: "kp"})
	out, err := runCompare(t, filepath.Join(first, ManifestName), second)
	if err == nil {
		t.Fatalf("the second build has no %s and the compare passed:\n%s", knoPpc64el, out)
	}
	if !strings.Contains(out, knoPpc64el) {
		t.Errorf("the failure does not name %s:\n%s", knoPpc64el, out)
	}
}

func TestCompareFailsWhenAnArchHasConfigsAndNoBinaries(t *testing.T) {
	files := map[string]string{
		kpAmd64: "kp", knoAmd64: "kno", cfgMixy: "mixy", cfgAnnares: "annares",
	}
	first, second := writeBuild(t, files), writeBuild(t, files)
	out, err := runCompare(t, filepath.Join(first, ManifestName), second)
	if err == nil {
		t.Fatalf("both builds ship a ppc64el config and no ppc64el binaries, and the compare passed:\n%s", out)
	}
	for _, want := range []string{"katzenpost_*_ppc64el.deb", "kno_*_ppc64el.deb", cfgAnnares} {
		if !strings.Contains(out, want) {
			t.Errorf("the failure does not name %s:\n%s", want, out)
		}
	}
}

func TestCompareDirsFailsWhenAFileIsGoneFromTheSecondBuild(t *testing.T) {
	files := map[string]string{kpAmd64: "kp", kpPpc64el: "kp-ppc"}
	first, second := writeBuild(t, files), writeBuild(t, files)
	if err := os.Remove(filepath.Join(second, kpPpc64el)); err != nil {
		t.Fatal(err)
	}
	out, err := runCompare(t, first, second)
	if err == nil {
		t.Fatalf("%s is not in the second build and the compare passed:\n%s", kpPpc64el, out)
	}
	if !strings.Contains(out, kpPpc64el+": in the reference, not in this build") {
		t.Errorf("the failure does not say that %s is missing:\n%s", kpPpc64el, out)
	}
}

func TestCompareDirsComparesTheToolPackages(t *testing.T) {
	first := writeBuild(t, map[string]string{kpAmd64: "kp", knoAmd64: "a", hostAnnares: "h"})
	second := writeBuild(t, map[string]string{kpAmd64: "kp", knoAmd64: "b", hostAnnares: "h"})
	out, err := runCompare(t, first, second)
	if err == nil {
		t.Fatalf("two different %s passed:\n%s", knoAmd64, out)
	}
	if !strings.Contains(out, knoAmd64+": digest") {
		t.Errorf("the failure does not name %s:\n%s", knoAmd64, out)
	}
}

func TestCompareDirsPassesTwoEqualBuildsAndCountsEveryFile(t *testing.T) {
	files := map[string]string{
		kpAmd64: "kp", kpPpc64el: "kp-ppc", knoAmd64: "kno", knoPpc64el: "kno-ppc",
		cfgMixy: "mixy", cfgAnnares: "annares", hostAnnares: "h",
		"katzenpost-0.0.107.pkg": "fbsd", "katzenpost-0.0.107-ubuntu26.04-ppc64el.tar.gz": "tgz",
	}
	first, second := writeBuild(t, files), writeBuild(t, files)
	out, err := runCompare(t, first, second)
	if err != nil {
		t.Fatalf("two equal builds failed: %v\n%s", err, out)
	}
	if want := "11 file(s)"; !strings.Contains(out, want) {
		t.Errorf("the report does not say %q, which is the 9 packages, %s and %s:\n%s",
			want, ManifestName, SumsName, out)
	}
}

func TestCompareDirsRefusesAnEmptyFirstBuild(t *testing.T) {
	out, err := runCompare(t, t.TempDir(), t.TempDir())
	if err == nil {
		t.Fatalf("two empty directories passed:\n%s", out)
	}
}

func TestCompareFailsWhenFreeBSDNodePackagesHaveNoKnoPkg(t *testing.T) {
	for _, node := range []string{
		"namenlos-config-respectmy-pq-authority-0.0.107.pkg",
		"kno-host-config-respectmy-0.0.107-1.pkg",
	} {
		files := map[string]string{"katzenpost-0.0.107.pkg": "fbsd", node: "cfg"}
		first, second := writeBuild(t, files), writeBuild(t, files)
		out, err := runCompare(t, first, second)
		if err == nil {
			t.Fatalf("%s with no kno .pkg passed:\n%s", node, out)
		}
		if !strings.Contains(out, "kno-*.pkg: not in this build, and "+node+" needs it") {
			t.Errorf("the failure does not name the kno .pkg:\n%s", out)
		}
	}
}
