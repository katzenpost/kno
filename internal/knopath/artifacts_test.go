package knopath

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stagePkgs(t *testing.T, root, tag string, names ...string) {
	t.Helper()
	dir := filepath.Join(root, "releases", tag, "pkgs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBuildsSkipsTagsWithoutPackages(t *testing.T) {
	art := t.TempDir()
	t.Setenv(artifactsEnv, art)
	if err := os.MkdirAll(filepath.Join(art, "releases", "v0.0.104", "pkgs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Builds(); len(got) != 0 {
		t.Errorf("builds = %+v, want none", got)
	}
}

func TestBuildsReadsOnlyTheArtifactsRoot(t *testing.T) {
	art, other := t.TempDir(), t.TempDir()
	t.Setenv(artifactsEnv, art)
	stagePkgs(t, art, "v0.0.104", "katzenpost_0.0.104-1_amd64.deb", "kno-host-config-mixy_0.0.104-1_all.deb")
	stagePkgs(t, other, "v0.0.105", "katzenpost_0.0.105-1_amd64.deb")

	got := Builds()
	if len(got) != 1 {
		t.Fatalf("got %d builds, want 1: %+v", len(got), got)
	}
	if got[0].Tag != "v0.0.104" || got[0].Root != art {
		t.Errorf("build = %+v, want v0.0.104 in %s", got[0], art)
	}
	if _, _, _, hc := got[0].Kinds(); !hc {
		t.Errorf("per-host config package not classified as host-config: %v", got[0].Packages)
	}

	t.Setenv(artifactsEnv, other)
	got = Builds()
	if len(got) != 1 || got[0].Tag != "v0.0.105" {
		t.Fatalf("after KNO_ARTIFACTS moved: %+v", got)
	}
}

func TestBuildsNewestTagFirst(t *testing.T) {
	art := t.TempDir()
	t.Setenv(artifactsEnv, art)
	stagePkgs(t, art, "v0.0.104", "kno_0.0.104-1_amd64.deb")
	stagePkgs(t, art, "v0.0.105", "kno_0.0.105-1_amd64.deb")
	got := Builds()
	if len(got) != 2 || got[0].Tag != "v0.0.105" {
		t.Fatalf("got %+v, want v0.0.105 first", got)
	}
}

func TestVersionLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{

		{"v0.0.99", "v0.0.104", true},
		{"v0.0.104", "v0.0.99", false},
		{"v0.0.104", "v0.0.105", true},
		{"v0.0.105", "v0.0.105-1", true},
		{"v0.0.105-1", "v0.0.105-2", true},
		{"v0.0.104", "v0.0.104", false},
		{"v0.1.0", "v0.0.999", false},
		{"v1.0.0", "v0.99.99", false},
	}
	for _, c := range cases {
		if got := VersionLess(c.a, c.b); got != c.want {
			t.Errorf("VersionLess(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestBuildsOrdersByVersionNotString(t *testing.T) {
	art := t.TempDir()
	t.Setenv(artifactsEnv, art)
	stagePkgs(t, art, "v0.0.99", "kno_0.0.99-1_amd64.deb")
	stagePkgs(t, art, "v0.0.104", "kno_0.0.104-1_amd64.deb")
	got := Builds()
	if len(got) != 2 || got[0].Tag != "v0.0.104" {
		t.Fatalf("got %+v, want v0.0.104 first", got)
	}
}

func TestSelfIsThePathSudoWasToldAbout(t *testing.T) {
	dir := t.TempDir()
	installed := filepath.Join(dir, "kno")
	if err := os.WriteFile(installed, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	notExec := filepath.Join(dir, "kno.not-executable")
	if err := os.WriteFile(notExec, []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "nowhere", "kno")
	const running = "/opt/kno/bin/kno"
	live := func() (string, error) { return running, nil }

	for _, tc := range []struct {
		name       string
		installed  string
		executable func() (string, error)
		want       string
	}{
		{"installed", installed, live, installed},
		{"not installed", missing, live, running},
		{"installed but not executable", notExec, live, running},
		{"replaced by the upgrade", missing, func() (string, error) {
			return running + " (deleted)", nil
		}, missing},
		{"no executable at all", missing, func() (string, error) {
			return "", os.ErrNotExist
		}, missing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := self(tc.installed, tc.executable); got != tc.want {
				t.Errorf("self = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFreeBSDCacheHonoursTheLegacyDirectory(t *testing.T) {
	for _, have := range []string{"base.txz", "base.qcow2"} {
		t.Run(have, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("KNO_FREEBSD_CACHE", "")
			legacy := filepath.Join(home, ".cache", "kno-freebsd")
			if err := os.MkdirAll(legacy, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(legacy, have), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := FreeBSDCache(); got != legacy {
				t.Errorf("with %s present, cache = %q, want %q", have, got, legacy)
			}
		})
	}
}

func TestFreeBSDCacheDefaultsToTheCacheRootWhenEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KNO_FREEBSD_CACHE", "")
	t.Setenv("KNO_CACHE", filepath.Join(home, "xdg"))
	if got, want := FreeBSDCache(), filepath.Join(home, "xdg", "freebsd"); got != want {
		t.Errorf("cache = %q, want %q", got, want)
	}
}

func TestFreeBSDCacheEnvWins(t *testing.T) {
	t.Setenv("KNO_FREEBSD_CACHE", "/somewhere/else")
	if got := FreeBSDCache(); got != "/somewhere/else" {
		t.Errorf("cache = %q, want the override", got)
	}
}

func TestReleasesAndStateStayPutForTheUpdaterAccount(t *testing.T) {
	t.Setenv("KNO_HOME", "")
	t.Setenv("KNO_ARTIFACTS", t.TempDir())
	if got, want := Releases(UpdaterHome), "/var/lib/kno-updater/releases"; got != want {
		t.Errorf("releases = %q, want %q", got, want)
	}
	if got, want := State(UpdaterHome), "/var/lib/kno-updater/state"; got != want {
		t.Errorf("state = %q, want %q", got, want)
	}

	t.Setenv("KNO_HOME", "/srv/kno-account")
	if got, want := Releases("/srv/kno-account"), "/srv/kno-account/releases"; got != want {
		t.Errorf("KNO_HOME releases = %q, want %q", got, want)
	}
}

func TestReleasesAndStateUseTheArtifactsRootForAPerson(t *testing.T) {
	art := t.TempDir()
	t.Setenv("KNO_HOME", "")
	t.Setenv("KNO_ARTIFACTS", art)
	home := t.TempDir()
	if got, want := Releases(home), filepath.Join(art, "releases"); got != want {
		t.Errorf("releases = %q, want %q", got, want)
	}
	if got, want := State(home), filepath.Join(art, "state"); got != want {
		t.Errorf("state = %q, want %q", got, want)
	}

	if strings.HasPrefix(State(home), Cache()) {
		t.Errorf("state = %q is under the cache root %q", State(home), Cache())
	}
}

func TestReleasesSitsUnderTheBuildRoot(t *testing.T) {
	t.Setenv("KNO_HOME", "")
	t.Setenv("KNO_ARTIFACTS", t.TempDir())
	for _, home := range []string{t.TempDir(), UpdaterHome} {
		rel := Releases(home)
		if filepath.Join(filepath.Dir(rel), "releases") != rel {
			t.Errorf("home %s: releases %q is not <buildroot>/releases", home, rel)
		}
	}
	if got, want := filepath.Dir(Releases(t.TempDir())), Dist(); got != want {
		t.Errorf("a person's build root = %q, want the one kno build uses (%q)", got, want)
	}
}

func TestRevisionTagsSortAboveTheBareTag(t *testing.T) {
	ordered := []string{
		"v0.0.104",
		"v0.0.104-1",
		"v0.0.104-9",
		"v0.0.104-10",
		"v0.0.104-141",
		"v0.0.105",
		"v0.0.105-1",
	}
	for i := 0; i+1 < len(ordered); i++ {
		a, b := ordered[i], ordered[i+1]
		if !VersionLess(a, b) {
			t.Errorf("%s should sort below %s", a, b)
		}
		if VersionLess(b, a) {
			t.Errorf("%s sorts below %s, which would target the wrong release", b, a)
		}
	}

	if !VersionLess("v0.0.99", "v0.0.104") {
		t.Error("v0.0.99 outranks v0.0.104")
	}
}

func TestSplitRevision(t *testing.T) {
	for in, want := range map[string][2]string{
		"v0.0.104":     {"v0.0.104", ""},
		"v0.0.104-141": {"v0.0.104", "141"},
		"v0.0.105-rc1": {"v0.0.105-rc1", ""},
		"v0.0.105-":    {"v0.0.105-", ""},
		"v0.0.105-1-2": {"v0.0.105-1", "2"},
		"no-dashes":    {"no-dashes", ""},
	} {
		ref, rev := SplitRevision(in)
		if ref != want[0] || rev != want[1] {
			t.Errorf("SplitRevision(%q) = (%q, %q), want (%q, %q)", in, ref, rev, want[0], want[1])
		}
	}
}

func TestRunningIsTheBinaryThatRuns(t *testing.T) {
	const checkout = "/Users/op/src/kno/bin/kno"
	if got := running(func() (string, error) { return checkout, nil }); got != checkout {
		t.Errorf("running = %q, want the checkout build %q, not an installed copy", got, checkout)
	}
	if got := running(func() (string, error) { return checkout + " (deleted)", nil }); got != Self() {
		t.Errorf("a replaced binary gave %q, want Self() %q", got, Self())
	}
}
