package build

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/pkg/deb"

	"github.com/katzenpost/kno/internal/testutil"
)

func repoWithPins(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, d := range []string{"thirdparty/patches", "networks/n"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "thirdparty", "pins.env"),
		[]byte("KATZENPOST_REF=v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "networks", "n", "manifest.toml"),
		[]byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func inputs(repo string) stampInputs {
	return stampInputs{Tag: "v1", Revision: "1", Network: "n",
		Arches: []string{"amd64"}, Services: []string{"a"}, Repo: repo}
}

func TestStampIsStableAndOrderIndependent(t *testing.T) {
	repo := repoWithPins(t)
	a := stamp(inputs(repo))
	if a == "" {
		t.Fatal("no stamp computed")
	}
	if b := stamp(inputs(repo)); a != b {
		t.Fatal("the same inputs must give the same stamp")
	}

	in := inputs(repo)
	in.Arches = []string{"amd64", "ppc64el"}
	one := stamp(in)
	in.Arches = []string{"ppc64el", "amd64"}
	if two := stamp(in); one != two {
		t.Fatal("arch order must not change the stamp")
	}
}

func TestStampChangesWithEveryInput(t *testing.T) {
	repo := repoWithPins(t)
	base := stamp(inputs(repo))

	cases := []struct {
		name  string
		apply func(*stampInputs)
	}{
		{"tag", func(i *stampInputs) { i.Tag = "v2" }},
		{"revision", func(i *stampInputs) { i.Revision = "2" }},
		{"network", func(i *stampInputs) { i.Network = "other" }},
		{"arches", func(i *stampInputs) { i.Arches = []string{"arm64"} }},
		{"services", func(i *stampInputs) { i.Services = []string{"b"} }},
		{"binary-only", func(i *stampInputs) { i.BinaryOnly = true }},
		{"warped", func(i *stampInputs) { i.Warped = true }},
		{"kno version", func(i *stampInputs) { i.KnoVersion = "0.0.107-1+20261020232341.gnew" }},
		{"epoch", func(i *stampInputs) { i.Epoch = "1780506605" }},
		{"peers", func(i *stampInputs) { i.Peers = map[string]string{"annares": "10.0.0.2:30000"} }},
		{"host config", func(i *stampInputs) { i.HostConfigs = map[string]string{"mixy": "aa"} }},
	}
	for _, c := range cases {
		in := inputs(repo)
		c.apply(&in)
		if got := stamp(in); got == base {
			t.Errorf("changing %s must change the stamp", c.name)
		}
	}

	if err := os.WriteFile(filepath.Join(repo, "thirdparty", "pins.env"),
		[]byte("KATZENPOST_REF=v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if stamp(inputs(repo)) == base {
		t.Error("a changed pin must change the stamp")
	}
	if err := os.WriteFile(filepath.Join(repo, "thirdparty", "pins.env"),
		[]byte("KATZENPOST_REF=v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if stamp(inputs(repo)) != base {
		t.Fatal("restoring the pin must restore the stamp")
	}

	if err := os.WriteFile(filepath.Join(repo, "thirdparty", "patches", "x.patch"),
		[]byte("diff\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if stamp(inputs(repo)) == base {
		t.Error("a new patch must change the stamp")
	}

	if err := os.WriteFile(filepath.Join(repo, "networks", "n", "manifest.toml"),
		[]byte("x = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if stamp(inputs(repo)) == base {
		t.Error("edited network data must change the stamp")
	}
}

func TestCachedRequiresBothStampAndDigests(t *testing.T) {
	rel := t.TempDir()
	pkgs := filepath.Join(rel, "pkgs")
	if err := os.MkdirAll(pkgs, 0o755); err != nil {
		t.Fatal(err)
	}
	deb := filepath.Join(pkgs, "a_1_all.deb")
	if err := os.WriteFile(deb, []byte("package bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum, _, err := fsutil.SHA256File(deb)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgs, "SHA256SUMS"),
		[]byte(sum+"  a_1_all.deb\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := cached(rel, "key"); got != nil {
		t.Fatalf("no stamp must mean no cache hit: %v", got)
	}
	if err := writeStampNote(rel, "key", nil); err != nil {
		t.Fatal(err)
	}
	if got := cached(rel, "key"); len(got) != 1 {
		t.Fatalf("stamp and digests agree; want one package, got %v", got)
	}

	if got := cached(rel, "other"); got != nil {
		t.Fatalf("a different stamp must miss: %v", got)
	}

	if err := os.WriteFile(deb, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := cached(rel, "key"); got != nil {
		t.Fatalf("a package that no longer matches its digest must not be reused: %v", got)
	}

	if err := os.WriteFile(deb, []byte("package bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := cached(rel, "key"); len(got) != 1 {
		t.Fatal("restoring the package should restore the hit")
	}
	if err := os.Remove(deb); err != nil {
		t.Fatal(err)
	}
	if got := cached(rel, "key"); got != nil {
		t.Fatalf("a missing package must not be reported as built: %v", got)
	}
}

func TestEmptyStampNeverMatches(t *testing.T) {
	rel := t.TempDir()
	if err := writeStampNote(rel, "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(rel, stampDir)); !os.IsNotExist(err) {
		t.Error("an empty key must write no stamp")
	}
	if got := cached(rel, ""); got != nil {
		t.Errorf("an empty key must never hit: %v", got)
	}
}

func TestStampIsEmptyWithoutPins(t *testing.T) {
	if got := stamp(inputs(t.TempDir())); got != "" {
		t.Errorf("no pins.env must yield no stamp, got %q", got)
	}
}

func TestStampIgnoresDeploymentRecords(t *testing.T) {
	repo := repoWithPins(t)
	dep := knopath.DeploymentsDir(repo, "n")
	if err := os.MkdirAll(dep, 0o755); err != nil {
		t.Fatal(err)
	}
	base := stamp(inputs(repo))
	if err := os.WriteFile(filepath.Join(dep, "annares.toml"),
		[]byte("deployed = \"v0.0.104\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := stamp(inputs(repo)); got != base {
		t.Fatal("a deployment record must not change the stamp: it is output, not input")
	}

	if err := os.WriteFile(filepath.Join(dep, "annares.toml"),
		[]byte("deployed = \"v0.0.104\"\nsucceeded = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := stamp(inputs(repo)); got != base {
		t.Fatal("rewriting a deployment record must not change the stamp")
	}

	if err := os.WriteFile(filepath.Join(repo, "networks", "n", "manifest.toml"),
		[]byte("x = 99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if stamp(inputs(repo)) == base {
		t.Fatal("edited network data must still change the stamp")
	}
	base = stamp(inputs(repo))

	rec := filepath.Join(knopath.GeneratedDir(repo, "n"), "releases", "v1")
	if err := os.MkdirAll(filepath.Join(rec, "nodes", "annares"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(rec, "SHA256SUMS"), filepath.Join(rec, "nodes", "annares", "SHA256SUMS")} {
		if err := os.WriteFile(p, []byte("aa  namenlos-config-annares-pq-authority_1_all.deb\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := stamp(inputs(repo)); got != base {
		t.Fatal("a release record must not change the stamp: kno build record writes it after the build")
	}
}

func TestTheRecordEntersTheKeyOnlyThroughWhatTheBuildReads(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "")
	repo := repoWithPins(t)
	o := Options{Repo: repo, Network: "n", Tag: "v1", Revision: "1", ToolRevision: "1",
		Services: []string{"a"}}
	base := stamp(o.stampIn(""))
	if base == "" {
		t.Fatal("no stamp computed")
	}
	man := filepath.Join(knopath.GeneratedDir(repo, "n"), "releases", "v1", "packages.json")
	if err := os.MkdirAll(filepath.Dir(man), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ body, why string }{
		{`{"tag":"v1","packages":[{"name":"a_1_all.deb","sha256":"aa"}]}`, ""},
		{`{"tag":"v1","source_date_epoch":"` + deb.DefaultEpoch + `","inputs":{"katzenpost_ref":"v1"}}`, ""},
		{`{"tag":"v1","source_date_epoch":"1780506605"}`, "the recorded epoch"},
		{`{"tag":"v1","inputs":{"hpqc_sha":"d836052c"}}`, "a recorded pin"},
	} {
		if err := os.WriteFile(man, []byte(c.body), 0o644); err != nil {
			t.Fatal(err)
		}
		got := stamp(o.stampIn(""))
		if c.why == "" && got != base {
			t.Errorf("the record %s changed the key; it gives the build no input", c.body)
		}
		if c.why != "" && got == base {
			t.Errorf("%s did not change the key; the build reads it from the record", c.why)
		}
	}
}

func TestTwoScopesOfOneReleaseBothStayCached(t *testing.T) {
	rel := t.TempDir()
	pkgs := filepath.Join(rel, "pkgs")
	if err := os.MkdirAll(pkgs, 0o755); err != nil {
		t.Fatal(err)
	}
	deb := filepath.Join(pkgs, "a_1_all.deb")
	if err := os.WriteFile(deb, []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum, _, err := fsutil.SHA256File(deb)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgs, "SHA256SUMS"),
		[]byte(sum+"  a_1_all.deb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeStampNote(rel, "scope-everything", nil); err != nil {
		t.Fatal(err)
	}
	if err := writeStampNote(rel, "scope-one-host", nil); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"scope-everything", "scope-one-host"} {
		if got := cached(rel, k); len(got) != 1 {
			t.Errorf("%s: want a hit, got %v", k, got)
		}
	}
	if got := cached(rel, "scope-never-built"); got != nil {
		t.Errorf("an unbuilt scope must miss: %v", got)
	}
}

func TestSupersededMatchesOnlyTheSamePackage(t *testing.T) {
	dir := t.TempDir()
	all := []string{
		"kno_0.0.104-153+dirty_ppc64el.deb",
		"kno_0.0.104-158_ppc64el.deb",
		"kno_0.0.104-159_ppc64el.deb",
		"kno_0.0.104-159_amd64.deb",
		"kno-host-config-annares_0.0.104-158_all.deb",
		"kno-host-config-annares_0.0.104-159_all.deb",
		"katzenpost_0.0.104_ppc64el.deb",
	}
	for _, f := range all {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got := superseded(filepath.Join(dir, "kno_0.0.104-159_ppc64el.deb"))
	want := map[string]bool{
		"kno_0.0.104-153+dirty_ppc64el.deb": true,
		"kno_0.0.104-158_ppc64el.deb":       true,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want exactly the older ppc64el knos", got)
	}
	for _, g := range got {
		if !want[filepath.Base(g)] {
			t.Errorf("%s is not superseded by this build", filepath.Base(g))
		}
	}

	got = superseded(filepath.Join(dir, "kno-host-config-annares_0.0.104-159_all.deb"))
	if len(got) != 1 || filepath.Base(got[0]) != "kno-host-config-annares_0.0.104-158_all.deb" {
		t.Errorf("got %v", got)
	}
}

func TestSplitDebName(t *testing.T) {
	for _, c := range []struct {
		in, name, arch string
		ok             bool
	}{
		{"kno_0.0.104-159_ppc64el.deb", "kno", "ppc64el", true},
		{"kno-host-config-annares_0.0.104-1_all.deb", "kno-host-config-annares", "all", true},
		{"SHA256SUMS", "", "", false},
		{"not-a-package.deb", "", "", false},
	} {
		name, arch, ok := splitDebName(c.in)
		if ok != c.ok || name != c.name || arch != c.arch {
			t.Errorf("%s: got (%q,%q,%v)", c.in, name, arch, ok)
		}
	}
}

func TestAnArchPackageRetiresItsArchitectureAllSelf(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{
		"namenlos-config-mixy-pq-mixserver_0.0.104-1_all.deb",
		"namenlos-config-mixy-pq-mixserver_0.0.104-2_amd64.deb",

		"namenlos-config-reed-pq-mixserver_0.0.104-1_all.deb",

		"namenlos-config-mixy-pq-mixserver-extra_0.0.104-1_all.deb",
	} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := superseded(filepath.Join(dir, "namenlos-config-mixy-pq-mixserver_0.0.104-2_amd64.deb"))
	var names []string
	for _, g := range got {
		names = append(names, filepath.Base(g))
	}
	want := "namenlos-config-mixy-pq-mixserver_0.0.104-1_all.deb"
	if len(names) != 1 || names[0] != want {
		t.Errorf("superseded = %v, want exactly [%s]", names, want)
	}

	got = superseded(filepath.Join(dir, "namenlos-config-reed-pq-mixserver_0.0.104-1_all.deb"))
	if len(got) != 0 {
		t.Errorf("an Architecture: all build retired %v", got)
	}
}

func TestTwoDistributionsOfOneArchDoNotSupersedeEachOther(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{
		"katzenpost_0.0.104+ubuntu24.04_amd64.deb",
		"katzenpost_0.0.104+ubuntu25.04_amd64.deb",
		"katzenpost_0.0.104+ubuntu26.04_ppc64el.deb",
	} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []string{
		"katzenpost_0.0.104+ubuntu24.04_amd64.deb",
		"katzenpost_0.0.104+ubuntu25.04_amd64.deb",
	} {
		if got := superseded(filepath.Join(dir, n)); len(got) != 0 {
			t.Errorf("%s superseded %v; they are different distributions", n, got)
		}
	}

	old := filepath.Join(dir, "katzenpost_0.0.103+ubuntu24.04_amd64.deb")
	if err := os.WriteFile(old, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := superseded(filepath.Join(dir, "katzenpost_0.0.104+ubuntu24.04_amd64.deb"))
	if len(got) != 1 || filepath.Base(got[0]) != "katzenpost_0.0.103+ubuntu24.04_amd64.deb" {
		t.Errorf("superseded = %v, want the older build of the same distribution", got)
	}
}

func TestDirtyIsNotADistribution(t *testing.T) {
	for _, c := range []struct{ base, want string }{
		{"katzenpost_0.0.104+ubuntu24.04_amd64.deb", "ubuntu24.04"},
		{"katzenpost_0.0.104-191+ubuntu24.04+dirty_amd64.deb", "ubuntu24.04"},
		{"kno_0.0.104-191+dirty_amd64.deb", ""},
		{"kno_0.0.104-191_amd64.deb", ""},

		{"katzenpost-warped_0.0.9+1191.gba728fe9-240_amd64.deb", ""},
		{"katzenpost-warped_0.0.9+1191.gba728fe9-255_amd64.deb", ""},
	} {
		if got := debOSToken(c.base); got != c.want {
			t.Errorf("debOSToken(%s) = %q, want %q", c.base, got, c.want)
		}
	}
}

func TestTwoWarpedBuildsSupersedeEachOther(t *testing.T) {
	dir := t.TempDir()
	old := "katzenpost-warped_0.0.9+1191.gba728fe9-240_amd64.deb"
	cur := "katzenpost-warped_0.0.9+1191.gba728fe9-255_amd64.deb"
	for _, n := range []string{old, cur} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := superseded(filepath.Join(dir, cur))
	if len(got) != 1 || filepath.Base(got[0]) != old {
		t.Fatalf("superseded = %v, want [%s]", got, old)
	}

	other := "katzenpost-warped_0.0.9+1191.gba728fe9-240_ppc64el.deb"
	if err := os.WriteFile(filepath.Join(dir, other), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := superseded(filepath.Join(dir, cur)); len(got) != 1 {
		t.Errorf("superseded = %v, want only the amd64 build", got)
	}
}

func TestTheToolRevisionInvalidatesTheCache(t *testing.T) {
	base := stampInputs{
		Tag: "v0.0.104", Network: "namenlos",
		Arches: []string{"amd64/ubuntu-24.04"}, Services: []string{"mixy-pq-mixserver"},
		Repo: testutil.RepoRoot(t), ToolRevision: "193",
	}

	if stamp(base) == "" {
		t.Fatal("the stamp could not be computed; the rest proves nothing")
	}
	moved := base
	moved.ToolRevision = "194"
	if stamp(base) == stamp(moved) {
		t.Error("a new tool revision did not invalidate the stamp; the build " +
			"would skip work push then refuses to deploy without")
	}

	dirty := base
	dirty.ToolRevision = "193+dirty"
	if stamp(base) == stamp(dirty) {
		t.Error("a dirty tool revision shared a stamp with the clean one")
	}

	if stamp(base) != stamp(base) {
		t.Error("the stamp is not stable for identical inputs")
	}
}

func TestABuildForEveryHostCoversABuildForOne(t *testing.T) {
	repo := repoWithPins(t)
	kno := filepath.Join(t.TempDir(), "kno")
	if err := os.WriteFile(kno, []byte("this kno"), 0o755); err != nil {
		t.Fatal(err)
	}
	rel := t.TempDir()
	pkgs := filepath.Join(rel, "pkgs")
	if err := os.MkdirAll(pkgs, 0o755); err != nil {
		t.Fatal(err)
	}
	deb := filepath.Join(pkgs, "a_1_all.deb")
	if err := os.WriteFile(deb, []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum, _, err := fsutil.SHA256File(deb)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgs, "SHA256SUMS"), []byte(sum+"  a_1_all.deb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wide := inputs(repo)
	wide.Kno = kno
	wide.Arches = []string{"amd64", "ppc64el"}
	wide.Services = []string{"a", "b", "c"}
	if err := writeStampNote(rel, stamp(wide), stampNote(wide, []string{"mixy", "reed", "annares"})); err != nil {
		t.Fatal(err)
	}

	one := inputs(repo)
	one.Kno = kno
	one.Services = []string{"b"}
	if got := covering(rel, one, []string{"reed"}); len(got) != 1 {
		t.Fatalf("the build for every host does not cover reed's build: %v", got)
	}
	if got := covering(rel, one, []string{"haywood"}); got != nil {
		t.Errorf("a host the wide build did not build for was covered: %v", got)
	}
	arm := one
	arm.Arches = []string{"arm64"}
	if got := covering(rel, arm, []string{"reed"}); got != nil {
		t.Errorf("an arch the wide build did not build was covered: %v", got)
	}
	other := one
	other.Tag = "v2"
	if got := covering(rel, other, []string{"reed"}); got != nil {
		t.Errorf("a build with other inputs was covered: %v", got)
	}
}

func TestReadyNeverBuilds(t *testing.T) {
	repo := repoWithPins(t)
	dist := t.TempDir()
	o := Options{Repo: repo, Network: "n", Tag: "v1", Dist: dist, Services: []string{"a"}}
	if _, err := Ready(o); !errors.Is(err, ErrNotBuilt) {
		t.Fatalf("Ready on a release nobody built = %v, want ErrNotBuilt", err)
	}
	if ents, _ := os.ReadDir(filepath.Join(dist, "releases", "v1")); len(ents) != 0 {
		t.Errorf("Ready wrote into the release directory: %v", ents)
	}
}

func TestCachedBuildReturnsOnlyItsOwnPackages(t *testing.T) {
	rel := t.TempDir()
	dir := filepath.Join(rel, "pkgs")
	current := "katzenpost_0.0.107+debian13_amd64.deb"
	old := "namenlos-config-gnunet-pq-gatewaynode_0.0.107-1+old_amd64.deb"
	currentSum := writePkg(t, dir, current, "current")
	oldSum := writePkg(t, dir, old, "old")
	if err := os.WriteFile(filepath.Join(dir, SumsName), []byte(currentSum+"  "+current+"\n"+oldSum+"  "+old+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeStampNote(rel, "current-build", nil, filepath.Join(dir, current)); err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		got := cached(rel, "current-build")
		if len(got) != 1 || got[0] != filepath.Join(dir, current) {
			t.Fatalf("cached build included unrelated packages: %v", got)
		}
	}
	check()
	writePkg(t, dir, old, "unrelated changes")
	check()
	writePkg(t, dir, current, "corrupted current")
	if got := cached(rel, "current-build"); got != nil {
		t.Fatalf("corrupted current package reused: %v", got)
	}
	writePkg(t, dir, current, "current")
	if err := os.WriteFile(filepath.Join(dir, SumsName), []byte(oldSum+"  "+old+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := cached(rel, "current-build"); got != nil {
		t.Fatalf("package missing from the manifest reused: %v", got)
	}
}
