package track

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/attest"
)

func TestRepoDirFindsAClonedKnoRepo(t *testing.T) {
	home := t.TempDir()
	clone := filepath.Join(home, "network-organizer")
	if err := os.MkdirAll(filepath.Join(clone, "data", "networks", "namenlos"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KNO_REPO", "")
	t.Setenv("HOME", home)
	if got := repoDir(home); got != filepath.Join(clone, "data") {
		t.Fatalf("repoDir = %q, want the clone's data root %q",
			got, filepath.Join(clone, "data"))
	}
}

func TestRepoDirResolvesKnoRepoToItsDataRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "data", "networks", "n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KNO_REPO", dir)
	if got := repoDir(dir); got != filepath.Join(dir, "data") {
		t.Fatalf("repoDir = %q, want %q", got, filepath.Join(dir, "data"))
	}
}

func TestEnsureReposClonesTheKnoRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	origin := t.TempDir()
	mkRepo(t, origin, "data/networks/namenlos/editable-configs/manifest.toml", "[network]\n")

	home := t.TempDir()
	e := Env{
		Home: home,
		Repo: CloneDir(home),

		KnoURL:         origin,
		KatzenpostRepo: filepath.Join(home, "katzenpost"),
	}
	if err := e.EnsureRepos(Logger{Tool: "t", W: io.Discard}, false); err != nil {
		t.Fatalf("EnsureRepos: %v", err)
	}
	if _, err := os.Stat(filepath.Join(CloneDir(home), ".git")); err != nil {
		t.Fatalf("no clone at %s: %v", CloneDir(home), err)
	}
	want := filepath.Join(CloneDir(home), "data")
	if e.Repo != want {
		t.Errorf("Repo = %q after the clone, want the data root %q", e.Repo, want)
	}
}

func mkRepo(t *testing.T, dir, file, body string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example",
			"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.hooksPath",
			"GIT_CONFIG_VALUE_0=/dev/null")
		if b, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	run("init", "-q", "-b", "main")
	p := filepath.Join(dir, file)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "seed")
}

func TestEnsureKnoRepoFastForwardsItsOwnClone(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	origin := t.TempDir()
	mkRepo(t, origin, "data/networks/n/editable-configs/manifest.toml", "[network]\n")

	home := t.TempDir()
	e := Env{Home: home, Repo: CloneDir(home), KnoURL: origin,
		KatzenpostRepo: filepath.Join(home, "katzenpost")}
	l := Logger{Tool: "t", W: io.Discard}
	if err := e.EnsureKnoRepo(l); err != nil {
		t.Fatal(err)
	}

	mkCommit(t, origin, "data/thirdparty/katzenpost-versions", "v0.0.105\n")

	if err := e.EnsureKnoRepo(l); err != nil {
		t.Fatal(err)
	}
	got := filepath.Join(CloneDir(home), "data", "thirdparty", "katzenpost-versions")
	b, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("the clone did not fast-forward: %v", err)
	}
	if string(b) != "v0.0.105\n" {
		t.Errorf("clone has %q, want the upstream content", b)
	}
}

func TestEnsureKnoRepoLeavesLocalChangesAlone(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	origin := t.TempDir()
	mkRepo(t, origin, "data/networks/n/editable-configs/manifest.toml", "[network]\n")
	home := t.TempDir()
	e := Env{Home: home, Repo: CloneDir(home), KnoURL: origin,
		KatzenpostRepo: filepath.Join(home, "katzenpost")}
	l := Logger{Tool: "t", W: io.Discard}
	if err := e.EnsureKnoRepo(l); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(CloneDir(home), "data", "networks", "n", "editable-configs", "manifest.toml")
	if err := os.WriteFile(mine, []byte("[network]\nmine = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mkCommit(t, origin, "data/networks/n/editable-configs/manifest.toml", "[network]\nupstream = 1\n")
	if err := e.EnsureKnoRepo(l); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(mine)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "[network]\nmine = 1\n" {
		t.Errorf("an update overwrote the operator's edit: %q", b)
	}
}

func TestEnsureKnoRepoFastForwardsPastALandedDeploymentRecord(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	origin := t.TempDir()
	rec := "data/networks/n/.generated/deployments/h.toml"
	mkRepo(t, origin, rec, "host = \"h\"\n")
	home := t.TempDir()
	e := Env{Home: home, Repo: CloneDir(home), KnoURL: origin,
		KatzenpostRepo: filepath.Join(home, "katzenpost")}
	l := Logger{Tool: "t", W: io.Discard}
	if err := e.EnsureKnoRepo(l); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(CloneDir(home), rec),
		[]byte("host = \"h\"\nlanded = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mkCommit(t, origin, "data/thirdparty/katzenpost-versions", "v0.0.105\n")
	if err := e.EnsureKnoRepo(l); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(CloneDir(home), "data", "thirdparty",
		"katzenpost-versions")); err != nil {
		t.Errorf("a changed deployment record stopped the fast-forward: %v", err)
	}
}

func mkCommit(t *testing.T, dir, file, body string) {
	t.Helper()
	p := filepath.Join(dir, file)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "upstream"}} {
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example",
			"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.hooksPath",
			"GIT_CONFIG_VALUE_0=/dev/null")
		if b, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
}

func TestKnoURLIsPassedThroughVerbatim(t *testing.T) {
	for _, u := range []string{
		"git@github.com:katzenpost/kno.git",
		"ssh://git@github.com/katzenpost/kno",
		"https://github.com/katzenpost/kno",
		"file:///srv/kno.git",
	} {
		t.Setenv("KNO_URL", u)
		if got := KnoURL(t.TempDir()); got != u {
			t.Errorf("KnoURL = %q, want %q", got, u)
		}
	}
}

func TestTrackClonesKatzenpostWhenThereIsNoClone(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	kp := t.TempDir()
	mkRepo(t, kp, "README", "katzenpost\n")
	tag := exec.Command("git", "-C", kp, "tag", "v0.0.105")
	if b, err := tag.CombinedOutput(); err != nil {
		t.Fatalf("git tag: %v\n%s", err, b)
	}

	kno := t.TempDir()
	mkRepo(t, kno, "data/thirdparty/katzenpost-versions", "v0.0.105\n")
	if err := os.MkdirAll(filepath.Join(kno, "data", "networks", "n"), 0o755); err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KNO_HOME", home)
	t.Setenv("KNO_REPO", kno)
	conf := filepath.Join(home, "kno.toml")
	if err := os.WriteFile(conf, []byte("network = \"n\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KNO_CONFIG", conf)
	t.Setenv("KNO_UPDATER_CONF", filepath.Join(home, "absent.conf"))
	t.Setenv("KATZENPOST_URL", kp)
	t.Setenv("KATZENPOST_REPO", "")
	t.Setenv("UPDATER_STATE_DIR", filepath.Join(home, "state"))
	t.Setenv("KNO_GIT_FETCH", "0")

	var out strings.Builder
	var errb strings.Builder
	if rc := Run(&out, &errb, nil); rc != 0 {
		t.Log(errb.String())
		t.Fatalf("kno track exited %d with no katzenpost clone; it must clone one", rc)
	}
	if got := strings.TrimSpace(out.String()); got != "v0.0.105" {
		t.Errorf("target %q, want v0.0.105", got)
	}
	if _, err := os.Stat(filepath.Join(home, "katzenpost", ".git")); err != nil {
		t.Errorf("no katzenpost clone at %s: %v", filepath.Join(home, "katzenpost"), err)
	}
}

func TestFetchKeepsALocalTag(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	up := t.TempDir()
	mkRepo(t, up, "README", "katzenpost\n")
	home := t.TempDir()
	kp := filepath.Join(home, "katzenpost")
	if b, err := exec.Command("git", "clone", "-q", up, kp).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, b)
	}
	if b, err := exec.Command("git", "-C", kp, "tag", "v0.0.999").CombinedOutput(); err != nil {
		t.Fatalf("tag: %v\n%s", err, b)
	}
	kno := t.TempDir()
	mkRepo(t, kno, "data/networks/n/manifest.toml", "[network]\n")
	t.Setenv("KNO_GIT_FETCH", "1")
	e := Env{Home: home, Repo: filepath.Join(kno, "data"), KatzenpostRepo: kp, KatzenpostURL: up}
	if err := e.EnsureRepos(Logger{Tool: "t", W: io.Discard}, true); err != nil {
		t.Fatal(err)
	}
	if !tagInRepo(kp, "v0.0.999") {
		t.Error("the fetch deleted a tag that exists only in this clone")
	}
}

func TestAFetchToolPathDoesNotTurnOffTheCloneRefresh(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("KNO_FETCH", "/usr/bin/kpfetch")
	origin := t.TempDir()
	mkRepo(t, origin, "data/networks/n/manifest.toml", "[network]\n")
	home := t.TempDir()
	e := Env{Home: home, Repo: CloneDir(home), KnoURL: origin,
		KatzenpostRepo: filepath.Join(home, "katzenpost")}
	l := Logger{Tool: "t", W: io.Discard}
	if err := e.EnsureKnoRepo(l); err != nil {
		t.Fatal(err)
	}
	mkCommit(t, origin, "data/thirdparty/katzenpost-versions", "v0.0.107\n")
	if err := e.EnsureKnoRepo(l); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(CloneDir(home), "data", "thirdparty", "katzenpost-versions")); err != nil {
		t.Fatal("KNO_FETCH, the fetch tool's path, stopped kno refreshing its own clone")
	}
}

func TestACloneURLCannotBecomeAnOption(t *testing.T) {
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	origin := t.TempDir()
	mkRepo(t, origin, "data/networks/namenlos/editable-configs/manifest.toml", "[network]\n")
	bin := t.TempDir()
	log := filepath.Join(bin, "log")
	wrapper := "#!/bin/sh\necho \"$@\" >> " + log + "\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("KNO_GIT_FETCH", "0")
	t.Setenv("KNO_CLONE_KP", "1")

	home := t.TempDir()
	e := Env{Home: home, Repo: CloneDir(home), KnoURL: origin,
		KatzenpostURL: origin, KatzenpostRepo: filepath.Join(home, "katzenpost")}
	if err := e.EnsureRepos(Logger{Tool: "t", W: io.Discard}, true); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"clone --quiet -- " + origin + " " + CloneDir(home),
		"clone --quiet -- " + origin + " " + e.KatzenpostRepo,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("git did not get %q:\n%s", want, b)
		}
	}
}

func TestRequiredSignaturesHoldAnUnsignedCommitAndTheDefaultReportsIt(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	signers := filepath.Join(t.TempDir(), "signers")
	if err := os.WriteFile(signers, []byte("op ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIC4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := attest.SignersPath
	attest.SignersPath = signers
	defer func() { attest.SignersPath = old }()

	for _, require := range []bool{false, true} {
		origin := t.TempDir()
		mkRepo(t, origin, "data/networks/n/editable-configs/manifest.toml", "[network]\n")
		home := t.TempDir()
		e := Env{Home: home, Repo: CloneDir(home), KnoURL: origin,
			KatzenpostRepo: filepath.Join(home, "katzenpost"), RequireSignatures: require}
		var log strings.Builder
		l := Logger{Tool: "t", W: &log}
		if err := e.EnsureKnoRepo(l); err != nil {
			t.Fatal(err)
		}
		mkCommit(t, origin, "data/thirdparty/katzenpost-versions", "v0.0.105\n")
		if err := e.EnsureKnoRepo(l); err != nil {
			t.Fatal(err)
		}
		_, err := os.Stat(filepath.Join(CloneDir(home), "data", "thirdparty", "katzenpost-versions"))
		if moved := err == nil; moved == require {
			t.Errorf("require_signatures=%v: fast-forwarded=%v", require, moved)
		}
		if !strings.Contains(log.String(), "unsigned") {
			t.Errorf("require_signatures=%v: the result is not reported:\n%s", require, log.String())
		}
	}
}
