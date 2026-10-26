package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/source"

	"github.com/katzenpost/kno/internal/parallel"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/testutil"
)

var stampRE = regexp.MustCompile(`\+\d{14}\.g[0-9a-f]+(\.dirty)?`)

func TestBuildAssemblesDebSet(t *testing.T) {
	repo := testutil.RepoRoot(t)
	tmp := t.TempDir()
	tag := pinnedTag(t, repo)
	ver := strings.TrimPrefix(tag, "v")

	cap := filepath.Join(tmp, "cap")
	if err := os.MkdirAll(cap, 0o755); err != nil {
		t.Fatal(err)
	}
	wrap := filepath.Join(tmp, "capture.sh")
	if err := os.WriteFile(wrap, []byte("#!/bin/sh\ncp -a \"$1\" \"$CAP/$(basename \"$2\")\"\nmkdir -p \"$(dirname \"$2\")\" && : > \"$2\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEB_BUILD", wrap)
	t.Setenv("CAP", cap)
	t.Setenv("KNO_BIN", "/nonexistent-kno")

	dist := filepath.Join(tmp, "dist")
	work := filepath.Join(tmp, "work")
	upstream := fakeUpstreamDeb(t, filepath.Join(work, "upstream-amd64-debian13", "tree", "dist"), ver, "amd64")

	svcs := []string{"luscus-pq-authority", "oneeyedman-pq-mixserver"}

	pkgs, err := Build(Options{
		Repo: repo, Network: "namenlos", Tag: tag, Dist: dist,
		Arches: []string{"amd64"}, Services: svcs, SkipCompile: true,
		SkipFreeBSD: true, SkipDocker: true, ToolRevision: "7", WorkDir: work,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var got []string
	for _, p := range pkgs {
		got = append(got, filepath.Base(p))
	}
	sort.Strings(got)

	want := []string{
		"katzenpost_" + ver + "+debian13_amd64.deb",
		"kno-host-config_" + ver + "-7_all.deb",
		"namenlos-config-luscus-pq-authority_" + ver + "-7_amd64.deb",
		"namenlos-config-oneeyedman-pq-mixserver_" + ver + "-7_arm64.deb",
	}
	sort.Strings(want)

	bare := make([]string, len(got))
	for i, g := range got {
		bare[i] = stampRE.ReplaceAllString(g, "")
	}
	if strings.Join(bare, ",") != strings.Join(want, ",") {
		t.Fatalf("deb set differs:\n  got:  %v\n  want: %v", bare, want)
	}
	for _, g := range got {
		stamped := stampRE.MatchString(g)
		upstream := strings.HasPrefix(g, "katzenpost_")
		if !upstream && !stamped {
			t.Errorf("%s carries no commit, so a changed input would keep its version", g)
		}
		if upstream && stamped {
			t.Errorf("%s carries a commit of this repo; katzenpost's version is its own", g)
		}
	}

	for _, w := range got {
		if strings.HasPrefix(w, "katzenpost_") {
			continue
		}
		ctl := filepath.Join(cap, w, "DEBIAN", "control")
		if _, err := os.Stat(ctl); err != nil {
			t.Errorf("captured root %s missing DEBIAN/control", w)
		}
	}
	want0, err := os.ReadFile(upstream)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pkgs {
		if strings.HasPrefix(filepath.Base(p), "katzenpost_") {
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != string(want0) {
				t.Errorf("%s is not the deb katzenpost's own packaging built", filepath.Base(p))
			}
		}
	}
	t.Logf("orchestrator produced %d debs", len(pkgs))
}

func TestGomaxprocsSizing(t *testing.T) {
	cpus := runtime.NumCPU()
	if got, want := parallel.GOMAXPROCS(cpus, 1), cpus; got != want {
		t.Errorf("one task should get every cpu: got %d want %d", got, want)
	}
	if cpus >= 16 {
		if got := parallel.GOMAXPROCS(cpus, 8); got < 2 {
			t.Errorf("8 tasks on %d cpus should get >1 core each, got %d", cpus, got)
		}
	}

	if got, want := parallel.GOMAXPROCS(4, 1000), max(runtime.NumCPU()/4, 1); got != want {
		t.Errorf("task count beyond the cap should size by the cap: got %d want %d", got, want)
	}

	if got := parallel.GOMAXPROCS(cpus*100, cpus*100); got != 1 {
		t.Errorf("oversubscribed pool should floor at 1, got %d", got)
	}
	if got := parallel.GOMAXPROCS(4, 0); got < 1 {
		t.Errorf("zero tasks must not divide by zero, got %d", got)
	}
}

func TestContentIDsAreKeptPerPackageNotPerName(t *testing.T) {
	ids := &contentIDs{}
	for _, c := range []struct{ os, arch, id string }{
		{"ubuntu24.04", "amd64", "a"},
		{"ubuntu25.04", "amd64", "b"},
		{"ubuntu26.04", "ppc64el", "c"},
	} {
		ids.note(deb.PkgID{Name: "katzenpost", Arch: c.arch, OS: c.os}, c.id)
	}
	all := ids.all()
	if len(all) != 3 {
		t.Fatalf("recorded %d content id(s) for three packages: %v", len(all), all)
	}

	for file, want := range map[string]string{
		"katzenpost_0.0.104+ubuntu24.04_amd64.deb":   "a",
		"katzenpost_0.0.104+ubuntu25.04_amd64.deb":   "b",
		"katzenpost_0.0.104+ubuntu26.04_ppc64el.deb": "c",
	} {
		if got := all[ID(file)]; got != want {
			t.Errorf("%s: content id %q, want %q", file, got, want)
		}
	}
}

func pinnedTag(t *testing.T, repo string) string {
	t.Helper()
	pins, err := source.ParsePins(filepath.Join(repo, "thirdparty", "pins.env"))
	if err != nil {
		t.Fatal(err)
	}
	if pins["KATZENPOST_REF"] == "" {
		t.Fatal("pins.env names no KATZENPOST_REF")
	}
	return pins["KATZENPOST_REF"]
}

func fakeUpstreamDeb(t *testing.T, dist, ver, arch string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "root")
	for _, d := range []string{"DEBIAN", "usr/bin"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, b := range []string{"kpserver", "kpdirauth", "kpreplica", "kpcourier", "kpclientd"} {
		if err := os.WriteFile(filepath.Join(root, "usr/bin", b), []byte("BIN:"+b+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ctl := "Package: katzenpost\nVersion: " + ver + "\nArchitecture: " + arch +
		"\nMaintainer: t <t@example>\nDescription: t\n"
	if err := os.WriteFile(filepath.Join(root, "DEBIAN", "control"), []byte(ctl), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dist, "katzenpost_"+ver+"_"+arch+".deb")
	if b, err := exec.Command("dpkg-deb", "--root-owner-group", "--build", root, out).CombinedOutput(); err != nil {
		t.Skipf("dpkg-deb cannot build a test package here: %v\n%s", err, b)
	}
	return out
}
