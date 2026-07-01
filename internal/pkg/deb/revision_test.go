package deb

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/release"
	"github.com/katzenpost/kno/internal/testutil"
)

func TestSplitRevision(t *testing.T) {
	cases := []struct{ in, ref, rev string }{
		{"v0.0.105-1", "v0.0.105", "1"},
		{"v0.0.105-12", "v0.0.105", "12"},
		{"v0.0.105", "v0.0.105", ""},

		{"v0.0.105-rc1", "v0.0.105-rc1", ""},
		{"v0.0.105-", "v0.0.105-", ""},
	}
	for _, c := range cases {
		ref, rev := SplitRevision(c.in)
		if ref != c.ref || rev != c.rev {
			t.Errorf("SplitRevision(%q) = (%q, %q), want (%q, %q)", c.in, ref, rev, c.ref, c.rev)
		}
	}
}

func TestVersion(t *testing.T) {
	if got := Version("v0.0.104", ""); got != "0.0.104" {
		t.Errorf("unrevisioned: got %q", got)
	}
	if got := Version("v0.0.104", "1"); got != "0.0.104-1" {
		t.Errorf("revisioned: got %q", got)
	}
}

func TestRevisionActuallyUpgrades(t *testing.T) {
	if _, err := exec.LookPath("dpkg"); err != nil {
		t.Skip("dpkg not available")
	}
	cmp := func(a, op, b string) bool {
		return exec.Command("dpkg", "--compare-versions", a, op, b).Run() == nil
	}
	base := Version("v0.0.104", "")
	if !cmp(base, "eq", Version("v0.0.104", "0")) {
		t.Error("expected dpkg to treat -0 as equal to no revision")
	}
	if !cmp(base, "lt", Version("v0.0.104", "1")) {
		t.Errorf("%s must upgrade to %s", base, Version("v0.0.104", "1"))
	}
	if !cmp(Version("v0.0.104", "9"), "lt", Version("v0.0.104", "10")) {
		t.Error("revisions must compare numerically, not lexically")
	}
	if !cmp(Version("v0.0.104", "1"), "lt", Version("v0.0.105", "")) {
		t.Error("a later upstream version must supersede any revision of the earlier one")
	}
}

func TestRevisionedControlIsConsistent(t *testing.T) {
	repo := testutil.RepoRoot(t)
	tmp := t.TempDir()
	tag, rev, svc := "v0.0.104", "1", "oneeyedman-pq-mixserver"
	t.Setenv("KNO_BIN", "/nonexistent-kno")

	rel := filepath.Join(tmp, "rel", tag)
	if err := release.Stage(repo, "namenlos", rel, []string{svc}, nil); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	root, err := AssembleConfigRoot(rel, tag, rev, svc, filepath.Join(tmp, "roots"), "amd64")
	if err != nil {
		t.Fatalf("AssembleConfigRoot: %v", err)
	}
	ctl, err := os.ReadFile(filepath.Join(root, "DEBIAN", "control"))
	if err != nil {
		t.Fatal(err)
	}
	want := Version(tag, rev)
	if !strings.Contains(string(ctl), "Version: "+want+"\n") {
		t.Errorf("control does not carry Version: %s\n%s", want, ctl)
	}
	if !strings.Contains(string(ctl), "Depends: katzenpost (= 0.0.104)\n") {
		t.Errorf("control does not pin katzenpost at upstream's version 0.0.104\n%s", ctl)
	}
}

func TestAConfigPackagePinsUpstreamsVersionNotItsOwn(t *testing.T) {
	repo := testutil.RepoRoot(t)
	tmp := t.TempDir()
	tag, svc := "v0.0.104", "oneeyedman-pq-mixserver"
	t.Setenv("KNO_BIN", "/nonexistent-kno")

	rel := filepath.Join(tmp, "rel", tag)
	if err := release.Stage(repo, "namenlos", rel, []string{svc}, nil); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	root, err := AssembleConfig(ConfigRoot{
		Rel: rel, Tag: tag, Rev: "255", BinRev: "", Svc: svc,
		RootParent: filepath.Join(tmp, "roots"), Arch: "arm64", OS: "debian-13",
	})
	if err != nil {
		t.Fatalf("AssembleConfig: %v", err)
	}
	ctl, err := os.ReadFile(filepath.Join(root, "DEBIAN", "control"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ctl), "Version: 0.0.104-255\n") {
		t.Errorf("control does not carry its own revision\n%s", ctl)
	}
	if !strings.Contains(string(ctl), "Depends: katzenpost (= 0.0.104)\n") {
		t.Errorf("control does not pin katzenpost's own version; katzenpost's debian/"+
			" names it 0.0.104 on every OS\n%s", ctl)
	}
}

func pinnedRef(t *testing.T, repo string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repo, "thirdparty", "pins.env"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "KATZENPOST_REF="); ok {
			return strings.Trim(v, `"`)
		}
	}
	t.Fatal("no KATZENPOST_REF in thirdparty/pins.env")
	return ""
}

func TestRepoRevisionStartsAtOne(t *testing.T) {
	got := RepoRevision(testutil.RepoRoot(t), "v0.0.104")

	count, _, _ := strings.Cut(got, "+")
	n, err := strconv.Atoi(count)
	if err != nil {
		t.Fatalf("revision %q is not a number: %v", got, err)
	}
	if n < 1 {
		t.Fatalf("revision %d must be at least 1; -0 compares equal to no revision", n)
	}
}

func TestRepoRevisionFallsBackWithoutGit(t *testing.T) {

	if got := RepoRevision(t.TempDir(), "v0.0.104"); got != "1" {
		t.Fatalf("outside a git repo want revision 1, got %q", got)
	}

	got := RepoRevision(testutil.RepoRoot(t), "v99.99.99-nonexistent")
	if count, _, _ := strings.Cut(got, "+"); count != "1" {
		t.Fatalf("unknown tag want revision 1, got %q", got)
	}
	if got != "1" && got != "1+dirty" {
		t.Fatalf("unknown tag gave %q, want 1 or 1+dirty", got)
	}
}

func TestTheRevisionCountsTheSharedBranch(t *testing.T) {
	needGitFor(t)
	origin := t.TempDir()
	run(t, "", "git", "init", "-q", "--bare", origin)

	work := t.TempDir()
	run(t, "", "git", "clone", "-q", origin, work)
	gitc := func(args ...string) { run(t, work, "git", args...) }
	gitc("config", "user.name", "t")
	gitc("config", "core.hooksPath", "/dev/null")
	gitc("config", "user.email", "t@t")
	gitc("commit", "-q", "--allow-empty", "-m", "base")
	gitc("tag", "v0.0.104")
	gitc("commit", "-q", "--allow-empty", "-m", "one")
	gitc("commit", "-q", "--allow-empty", "-m", "two")
	gitc("branch", "-M", "main")
	gitc("push", "-q", "-u", "origin", "main")

	if got := RepoRevision(work, "v0.0.104"); got != "3" {
		t.Fatalf("RepoRevision = %q, want 3", got)
	}

	gitc("checkout", "-q", "-b", "side")
	gitc("commit", "-q", "--allow-empty", "-m", "side work")
	gitc("checkout", "-q", "main")
	gitc("merge", "-q", "--no-ff", "-m", "merge side", "side")
	got := RepoRevision(work, "v0.0.104")
	if !strings.HasPrefix(got, "3") {
		t.Errorf("a local merge changed the shared count: %q", got)
	}
	if strings.HasSuffix(got, "+dirty") {
		t.Errorf("committed work that the shared branch has not seen is marked dirty: %q", got)
	}

	gitc("push", "-q", "origin", "main")
	clean := RepoRevision(work, "v0.0.104")
	if strings.Contains(clean, "dirty") {
		t.Fatalf("a pushed, clean tree is marked dirty: %q", clean)
	}
	if err := os.WriteFile(filepath.Join(work, "scratch"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := RepoRevision(work, "v0.0.104"); got != clean+"+dirty" {
		t.Errorf("uncommitted work gave %q, want %s+dirty", got, clean)
	}
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, b)
	}
}

func needGitFor(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

func TestRepoRevisionAcceptsTagWithOrWithoutV(t *testing.T) {
	repo := testutil.RepoRoot(t)
	if a, b := RepoRevision(repo, "v0.0.104"), RepoRevision(repo, "0.0.104"); a != b {
		t.Fatalf("v-prefix changed the revision: %q vs %q", a, b)
	}
}

func TestRevisionOfInvertsOSVersion(t *testing.T) {
	for _, c := range []struct{ tag, rev, os string }{

		{"v0.0.104", "", "ubuntu-25.04"},
		{"v0.0.104", "", ""},
		{"v0.0.104", "198", "ubuntu-24.04"},
		{"v0.0.104", "198", "ubuntu-25.04"},
		{"v0.0.104", "198+dirty", "ubuntu-24.04"},
		{"v0.0.104", "1", "debian-13"},
		{"v0.0.104", "198", ""},
		{"v0.0.104", "198+dirty", ""},
	} {
		v := OSVersion(c.tag, c.rev, c.os)
		got, ok := RevisionOf(v, c.tag, c.os)
		if !ok {
			t.Errorf("RevisionOf(%q, %q, %q) could not invert its own output", v, c.tag, c.os)
			continue
		}
		if got != c.rev {
			t.Errorf("RevisionOf(%q, %q, %q) = %q, want %q", v, c.tag, c.os, got, c.rev)
		}
		if back := OSVersion(c.tag, got, c.os); back != v {
			t.Errorf("round trip: %q -> %q -> %q", v, got, back)
		}
	}

	for _, bad := range []struct{ v, tag, os string }{
		{"1.2.3-4", "v0.0.104", ""},
		{"0.0.104-198+ubuntu24.04", "v0.0.104", "ubuntu-25.04"},
		{"", "v0.0.104", ""},
	} {
		if got, ok := RevisionOf(bad.v, bad.tag, bad.os); ok || got != "" {
			t.Errorf("RevisionOf(%q, %q, %q) = (%q, %v), want (\"\", false)",
				bad.v, bad.tag, bad.os, got, ok)
		}
	}
}

func TestRepoRevisionMarksDirtyWithNoTagToCountFrom(t *testing.T) {
	repo := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.hooksPath",
			"GIT_CONFIG_VALUE_0=/dev/null",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	runGit("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "a"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "-A")
	runGit("commit", "-qm", "one")

	if got := RepoRevision(repo, "v9.9.9"); got != "1" {
		t.Errorf("clean tree with no such tag: revision %q, want \"1\"", got)
	}
	if err := os.WriteFile(filepath.Join(repo, "a"), []byte("2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := RepoRevision(repo, "v9.9.9"); got != "1+dirty" {
		t.Errorf("dirty tree with no such tag: revision %q, want \"1+dirty\"", got)
	}
}

func TestOSVersionOrdersAboveTheUnrevisionedForm(t *testing.T) {
	plain := OSVersion("v0.0.104", "", "ubuntu-24.04")
	if plain != "0.0.104+ubuntu24.04" {
		t.Fatalf("unrevisioned = %q", plain)
	}
	revved := OSVersion("v0.0.104", "166", "ubuntu-24.04")
	if revved != "0.0.104+ubuntu24.04-166" {
		t.Fatalf("revisioned = %q, want the revision last", revved)
	}
	for _, c := range []struct{ a, b string }{
		{revved, plain},
		{revved, "0.0.104-164"},
		{revved, "0.0.104-166"},
		{OSVersion("v0.0.104", "166+dirty", "ubuntu-24.04"), revved},
		{OSVersion("v0.0.104", "167", "ubuntu-24.04"),
			OSVersion("v0.0.104", "166+dirty", "ubuntu-24.04")},
		{OSVersion("v0.0.105", "", "ubuntu-24.04"),
			OSVersion("v0.0.104", "999", "ubuntu-24.04")},
	} {
		if CompareVersions(c.a, c.b) <= 0 {
			t.Errorf("NOT (%s gt %s)", c.a, c.b)
		}
	}
}

func TestOSVersionKeepsDirtyLast(t *testing.T) {
	got := OSVersion("v0.0.104", "1+dirty", "ubuntu-25.04")
	if got != "0.0.104+ubuntu25.04-1+dirty" {
		t.Errorf("OSVersion = %q", got)
	}
}

func TestRevisionOfInvertsTheNewComposition(t *testing.T) {
	for _, c := range []struct {
		rev, os string
	}{
		{"166", "ubuntu-24.04"},
		{"1+dirty", "ubuntu-25.04"},
		{"", "ubuntu-26.04"},
		{"263", ""},
		{"", ""},
	} {
		v := OSVersion("v0.0.104", c.rev, c.os)
		got, ok := RevisionOf(v, "v0.0.104", c.os)
		if !ok {
			t.Errorf("RevisionOf(%q) could not invert it", v)
			continue
		}
		if got != c.rev {
			t.Errorf("RevisionOf(%q) = %q, want %q", v, got, c.rev)
		}
	}
}

func gnuMake(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"gmake", "make"} {
		if _, err := exec.LookPath(name); err != nil {
			continue
		}
		if out, err := exec.Command(name, "--version").Output(); err == nil && strings.HasPrefix(string(out), "GNU Make") {
			return name
		}
	}
	t.Skip("no GNU make (gmake or make); the Makefile needs GNU make")
	return ""
}
