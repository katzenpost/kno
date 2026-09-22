package sim

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/probe"
	"github.com/katzenpost/kno/internal/testutil"
)

func TestKnownOrphansComeFromTheFindings(t *testing.T) {
	r := newRun(NetOptions{Repo: testutil.RepoRoot(t), Network: "namenlos", Work: t.TempDir()})
	if got := strings.Join(r.knownOrphans(), ","); got != "wauland2" {
		t.Errorf("known orphans %q, want wauland2", got)
	}
}

func TestEveryKnownOrphanReachesTheProbe(t *testing.T) {
	repo := t.TempDir()
	net := filepath.Join(repo, "networks", "n", "editable-configs")
	if err := os.MkdirAll(filepath.Join(net, "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(repo, "topology-findings.txt"): "aaa\nbbb\nplaced\n",
		filepath.Join(net, "manifest.toml"):          "[[topology.layer]]\nmixes = [\"placed\"]\n",
	}
	for _, m := range []string{"aaa", "bbb", "ccc", "placed"} {
		files[filepath.Join(net, "nodes", m+"-mix.toml")] = "name = \"" + m + "\"\nrole = \"mix\"\n"
	}
	for p, body := range files {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r := newRun(NetOptions{Repo: repo, Network: "n", Net: "t", Work: t.TempDir()})
	env := ""
	for _, a := range r.probeArgs() {
		if v, ok := strings.CutPrefix(a, "PROBE_KNOWN_ORPHANS="); ok {
			env = v
		}
	}
	got := probe.KnownOrphans(env)
	if len(got) != 2 || got[0] != "aaa" || got[1] != "bbb" {
		t.Errorf("the probe reads known orphans %v from %q, want aaa and bbb", got, env)
	}
}

func onAnAmd64Host(t *testing.T) {
	t.Helper()
	old := hostGoarch
	hostGoarch = "amd64"
	t.Cleanup(func() { hostGoarch = old })
}

func TestEmulationRunsEachNodeOnItsOwnPlatform(t *testing.T) {
	onAnAmd64Host(t)
	r := newRun(NetOptions{Repo: "../../data", Network: "namenlos", Net: "t", Work: t.TempDir(), Emulate: true})
	cases := map[string]platform{
		"annares":    {Goarch: "ppc64le", OS: "ubuntu-26.04"},
		"oneeyedman": {Goarch: "arm64", OS: "debian-13"},
		"mixy":       {Goarch: "amd64", OS: "ubuntu-25.04"},
		"haywood":    {Goarch: "amd64", OS: "ubuntu-24.04"},
	}
	for node, want := range cases {
		d := daemon{Arch: config.NodeArch(r.Repo, r.Network, node), OS: config.NodeOS(r.Repo, r.Network, node)}
		if got := r.platformOf(d); got != want {
			t.Errorf("%s runs on %s, want %s", node, got, want)
		}
	}
	r.Emulate = false
	d := daemon{Arch: "ppc64el", OS: "ubuntu-26.04"}
	if got, want := r.platformOf(d), r.clientPlatform(); got != want {
		t.Errorf("without --emulate annares runs on %s, want the client platform %s:"+
			" kno build makes no %s package for a ppc64el node", got, want, hostGoarch)
	}
	d = daemon{Arch: "amd64", OS: "ubuntu-25.04"}
	if got := r.platformOf(d); got.OS != "ubuntu-25.04" {
		t.Errorf("without --emulate a native node loses its OS: %s", got)
	}
}

func TestEveryPlatformOfTheNetworkHasAPinnedRunnerBase(t *testing.T) {
	r := newRun(NetOptions{Repo: "../../data", Network: "namenlos", Net: "t", Work: t.TempDir(), Emulate: true})
	ds, err := r.daemonsOf()
	if err != nil {
		t.Fatal(err)
	}
	plats := r.neededPlatforms(ds)
	if len(plats) < 5 {
		t.Errorf("namenlos needs %d platforms: %v; it has at least 5", len(plats), plats)
	}
	for _, p := range plats {
		if _, err := compile.BaseImageFor(p.Goarch, p.OS); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}

func TestTheDebForAPlatformIsThatPlatformsDeb(t *testing.T) {
	have := []string{
		"/x/katzenpost_0.0.105+ubuntu25.04_amd64.deb",
		"/x/katzenpost_0.0.105+ubuntu26.04_ppc64el.deb",
		"/x/katzenpost_0.0.105+debian13_amd64.deb",
		"/x/katzenpost_0.0.105+debian13_arm64.deb",
	}
	cases := map[platform]string{
		{Goarch: "ppc64le", OS: "ubuntu-26.04"}: "katzenpost_0.0.105+ubuntu26.04_ppc64el.deb",
		{Goarch: "amd64", OS: "ubuntu-25.04"}:   "katzenpost_0.0.105+ubuntu25.04_amd64.deb",
		{Goarch: "arm64", OS: "debian-13"}:      "katzenpost_0.0.105+debian13_arm64.deb",
	}
	for p, want := range cases {
		if got := filepath.Base(pickDeb(have, p)); got != want {
			t.Errorf("%s gets %s, want %s", p, got, want)
		}
	}
	if pickDeb(have, platform{Goarch: "amd64", OS: "ubuntu-24.04"}) != "" {
		t.Error("a platform with no package was given another platform's package")
	}
}

func TestNodeFilesAreTheNodesNotTheManifest(t *testing.T) {
	files, err := nodeFiles(knopath.NetworkDir("../../data", "namenlos"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if filepath.Base(f) == "manifest.toml" {
			t.Fatalf("the manifest was taken for a node file")
		}
	}
	if len(files) != 27 {
		t.Errorf("found %d node files, namenlos has 27", len(files))
	}
}

func TestPkgsMayBeTheRunsOwnPackageDirectory(t *testing.T) {
	onAnAmd64Host(t)
	r := testRun(t)
	if err := os.MkdirAll(r.pkgs, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"debian13_amd64", "ubuntu24.04_amd64", "ubuntu25.04_amd64"} {
		if err := os.WriteFile(filepath.Join(r.pkgs, "katzenpost_0.0.105+"+n+".deb"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r.Pkgs = r.pkgs + "/"
	if err := r.provisionPackages(); err != nil {
		t.Fatalf("provisionPackages: %v", err)
	}
	left, _ := filepath.Glob(filepath.Join(r.pkgs, "*.deb"))
	if len(left) != 3 {
		t.Fatalf("%d packages left in %s, want the 3 it was given", len(left), r.pkgs)
	}
}

func TestSimSaysItRunsOnLinuxOnly(t *testing.T) {
	for _, goos := range []string{"darwin", "freebsd"} {
		if err := linuxOnly(goos); err == nil || !strings.Contains(err.Error(), "Linux") {
			t.Errorf("%s: %v, want a refusal that names Linux", goos, err)
		}
	}
	if err := linuxOnly("linux"); err != nil {
		t.Errorf("linux was refused: %v", err)
	}
}
