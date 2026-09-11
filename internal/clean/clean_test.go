package clean

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/track"
)

func TestRemoveRefusesAnythingItDoesNotOwn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KNO_CACHE", filepath.Join(home, "cache"))
	t.Setenv("KNO_ARTIFACTS", filepath.Join(home, "artifacts"))

	victim := filepath.Join(home, "precious")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(victim, "identity.private.pem")
	if err := os.WriteFile(keep, []byte("a node's identity"), 0o600); err != nil {
		t.Fatal(err)
	}

	var b bytes.Buffer
	for _, p := range []string{
		victim,
		home,
		"/",
		"/etc",
		os.Getenv("HOME"),
		filepath.Join(home, "cache", "..", "precious"),
	} {
		err := Remove(&b, track.Env{}, []Item{{What: "not ours", Path: p, Rebuildable: true}}, false)
		if err == nil {
			t.Errorf("Remove accepted %q", p)
		}
		if err != nil && !strings.Contains(err.Error(), "kno owns") {
			t.Errorf("%q refused for the wrong reason: %v", p, err)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("a refused Remove deleted something anyway: %v", err)
	}
}

func TestTheUpdaterStateIsNeverRemovable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KNO_CACHE", filepath.Join(home, "cache"))
	t.Setenv("KNO_ARTIFACTS", filepath.Join(home, "artifacts"))
	state := filepath.Join(home, "artifacts", "state")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}

	items := Survey(track.Env{StateDir: state})
	var sawState bool
	for _, it := range items {
		if it.Path == state {
			sawState = true
			if it.Rebuildable {
				t.Error("the updater state is marked rebuildable")
			}
			if it.Why == "" {
				t.Error("the report does not say what would be lost")
			}
		}
	}
	if !sawState {
		t.Fatal("the survey does not mention the state directory at all")
	}

	for _, k := range []Kind{
		{Cache: true}, {Releases: true}, {Images: true},
		{Cache: true, Releases: true, Images: true},
	} {
		for _, it := range Removable(items, k) {
			if it.Path == state {
				t.Errorf("%+v offered the state directory for removal", k)
			}
		}
	}
}

func TestEachFlagSelectsOnlyItsOwn(t *testing.T) {
	home := t.TempDir()
	cache := filepath.Join(home, "cache")
	art := filepath.Join(home, "artifacts")
	t.Setenv("KNO_CACHE", cache)
	t.Setenv("KNO_ARTIFACTS", art)
	t.Setenv("KNO_FREEBSD_CACHE", filepath.Join(home, "fbsd"))

	items := Survey(track.Env{ReleasesDir: filepath.Join(art, "releases")})
	paths := func(k Kind) []string {
		var out []string
		for _, it := range Removable(items, k) {
			out = append(out, it.Path)
		}
		return out
	}

	releases := filepath.Join(art, "releases")
	got := paths(Kind{Releases: true})
	if len(got) != 1 || got[0] != releases {
		t.Errorf("--releases selects %v, want just %s", got, releases)
	}
	for _, p := range paths(Kind{Cache: true}) {
		if p == releases {
			t.Error("--cache reaches into the built releases")
		}
	}
	if len(paths(Kind{})) != 0 {
		t.Error("no flag removed something")
	}

	if len(paths(Kind{Images: true})) != 0 {
		t.Error("--images selected a filesystem path")
	}
}

func TestDryRunRemovesNothing(t *testing.T) {
	home := t.TempDir()
	cache := filepath.Join(home, "cache")
	t.Setenv("KNO_CACHE", cache)
	t.Setenv("KNO_ARTIFACTS", filepath.Join(home, "artifacts"))
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	canary := filepath.Join(cache, "built.o")
	if err := os.WriteFile(canary, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var b bytes.Buffer
	items := Removable(Survey(track.Env{}), Kind{Cache: true})
	if err := Remove(&b, track.Env{}, items, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(canary); err != nil {
		t.Fatalf("--dry-run deleted it: %v", err)
	}
	if !strings.Contains(b.String(), cache) {
		t.Errorf("a dry run did not say what it would take:\n%s", b.String())
	}

	b.Reset()
	if err := Remove(&b, track.Env{}, items, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(canary); !os.IsNotExist(err) {
		t.Errorf("the cache survived a real run: %v", err)
	}
}

func TestAMissingDirectoryIsZeroNotAFailure(t *testing.T) {
	if got := size(filepath.Join(t.TempDir(), "nothing-here")); got != 0 {
		t.Errorf("size of a missing tree = %d, want 0", got)
	}
	if got := size(""); got != -1 {
		t.Errorf("size of no path = %d, want -1 (unmeasurable)", got)
	}
}

func TestCleanKeepsWhatAHostCouldRollBackTo(t *testing.T) {
	art := t.TempDir()
	t.Setenv("KNO_ARTIFACTS", art)
	state := t.TempDir()

	rel := filepath.Join(art, "releases")
	for _, tag := range []string{"v0.0.102", "v0.0.103", "v0.0.104"} {
		if err := os.MkdirAll(filepath.Join(rel, tag, "pkgs"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	host := filepath.Join(state, "hosts", "mixy")
	if err := os.MkdirAll(host, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"current": "v0.0.104\n", "last-known-good": "v0.0.103\n",
	} {
		if err := os.WriteFile(filepath.Join(host, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e := track.Env{StateDir: state, CurrentFile: filepath.Join(state, "current"),
		ReleasesDir: rel,
		Config:      config.Config{Hosts: map[string]config.Host{"mixy": {}}}}

	var item Item
	for _, it := range Survey(e) {
		if strings.Contains(it.What, "releases") {
			item = it
		}
	}
	if strings.Join(item.Protected, " ") != "v0.0.103 v0.0.104" {
		t.Fatalf("protected = %v, want the running release and the rollback", item.Protected)
	}

	var out strings.Builder
	if err := Remove(&out, e, []Item{item}, false); err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"v0.0.103", "v0.0.104"} {
		if _, err := os.Stat(filepath.Join(rel, tag)); err != nil {
			t.Errorf("%s was removed; a host needs it", tag)
		}
	}
	if _, err := os.Stat(filepath.Join(rel, "v0.0.102")); err == nil {
		t.Error("v0.0.102 survived; nothing refers to it and it is rebuildable")
	}
}

func mkfile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("KNO_HOME", "")
	t.Setenv("KNO_CONFIG", "")
	t.Setenv("KNO_FREEBSD_CACHE", "")
	t.Setenv("KEYBACKUP_DIR", "")
	t.Setenv("RELEASES_DIR", "")
	t.Setenv("UPDATER_STATE_DIR", "")
	t.Setenv("CURRENT_FILE", "")
	t.Setenv("KNO_UPDATER_CONF", filepath.Join(home, "no-legacy.conf"))
	t.Setenv("KNO_ARTIFACTS", filepath.Join(home, "artifacts"))
	t.Setenv("KNO_CACHE", filepath.Join(home, "cache"))
	return home
}

func TestABrokenConfigStopsCleanBeforeItRemovesAnything(t *testing.T) {
	home := isolate(t)
	bad := filepath.Join(home, "kno.toml")
	mkfile(t, bad, "[hosts.mixy\nssh = \n")
	t.Setenv("KNO_CONFIG", bad)
	rel := filepath.Join(home, "artifacts", "releases")
	t.Setenv("RELEASES_DIR", rel)
	keep := filepath.Join(rel, "v0.0.104", "pkgs", "kno.deb")
	mkfile(t, keep, "x")

	cmd := Command()
	cmd.SetArgs([]string{"--releases"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err == nil {
		t.Fatal("clean ran with a kno.toml it could not read")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("clean removed a release with no config to say which hosts run it: %v", err)
	}
}

func TestAHostOnlyTheStateDirKnowsIsStillProtected(t *testing.T) {
	state := t.TempDir()
	mkfile(t, filepath.Join(state, "hosts", "reed", "current"), "v0.0.103\n")
	mkfile(t, filepath.Join(state, "hosts", "reed", "last-known-good"), "v0.0.102\n")
	got := protectedTags(track.Env{StateDir: state})
	if strings.Join(got, " ") != "v0.0.102 v0.0.103" {
		t.Errorf("protected = %v, want the state of a host that kno.toml no longer names", got)
	}
}

func TestRemoveRefusesAHomeOrACheckoutThatAnEnvVarNames(t *testing.T) {
	home := isolate(t)
	precious := filepath.Join(home, "precious.txt")
	mkfile(t, precious, "x")

	t.Setenv("KNO_CACHE", home)
	var b bytes.Buffer
	if err := Remove(&b, track.Env{}, Removable(Survey(track.Env{}), Kind{Cache: true}), false); err == nil {
		t.Error("KNO_CACHE=$HOME was removed")
	}

	checkout := filepath.Join(home, "src", "kno")
	mkfile(t, filepath.Join(checkout, ".git", "HEAD"), "ref: refs/heads/main\n")
	t.Setenv("KNO_CACHE", checkout)
	if err := Remove(&b, track.Env{}, Removable(Survey(track.Env{}), Kind{Cache: true}), false); err == nil {
		t.Error("a git checkout named by KNO_CACHE was removed")
	}

	data := filepath.Join(home, "data")
	mkfile(t, filepath.Join(data, "networks", "namenlos", "network.toml"), "x")
	t.Setenv("KNO_CACHE", data)
	if err := Remove(&b, track.Env{}, Removable(Survey(track.Env{}), Kind{Cache: true}), false); err == nil {
		t.Error("a tree that holds networks/ was removed")
	}

	art := filepath.Join(home, "artifacts")
	state := filepath.Join(art, "state")
	mkfile(t, filepath.Join(state, "hosts", "mixy", "current"), "v0.0.104\n")
	e := track.Env{StateDir: state, ReleasesDir: art}
	if err := Remove(&b, e, Removable(Survey(e), Kind{Releases: true}), false); err == nil {
		t.Error("RELEASES_DIR over the updater state was swept")
	}
	if _, err := os.Stat(filepath.Join(state, "hosts", "mixy", "current")); err != nil {
		t.Errorf("the updater state is gone: %v", err)
	}
	if _, err := os.Stat(precious); err != nil {
		t.Fatalf("a refused Remove deleted something anyway: %v", err)
	}
}

func TestRemoveStillTakesACacheThatHoldsClonesInside(t *testing.T) {
	home := isolate(t)
	cache := filepath.Join(home, "cache")
	mkfile(t, filepath.Join(cache, "build", ".src", "katzenpost", ".git", "HEAD"), "x")
	mkfile(t, filepath.Join(cache, "repo-0a274bb955228132", "networks", "n.toml"), "x")
	var b bytes.Buffer
	if err := Remove(&b, track.Env{}, Removable(Survey(track.Env{}), Kind{Cache: true}), false); err != nil {
		t.Fatalf("the real cache layout was refused: %v", err)
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Errorf("the cache survived: %v", err)
	}
}

func TestAKeptReleaseKeepsItsConfigSnapshot(t *testing.T) {
	isolate(t)
	rel := t.TempDir()
	state := t.TempDir()
	for _, d := range []string{"v0.0.102", "v0.0.102.last", "v0.0.104", "v0.0.104.last"} {
		mkfile(t, filepath.Join(rel, d, "pkgs", "x.deb"), "x")
	}
	mkfile(t, filepath.Join(state, "current"), "v0.0.104\n")
	e := track.Env{StateDir: state, CurrentFile: filepath.Join(state, "current"), ReleasesDir: rel}

	items := Removable(Survey(e), Kind{Releases: true})
	if len(items) != 1 || items[0].Path != rel {
		t.Fatalf("--releases selects %v, want the updater's releases dir %s", items, rel)
	}
	var b bytes.Buffer
	if err := Remove(&b, e, items, false); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"v0.0.104", "v0.0.104.last"} {
		if _, err := os.Stat(filepath.Join(rel, d)); err != nil {
			t.Errorf("%s was removed; update rolls the configs back from it", d)
		}
	}
	for _, d := range []string{"v0.0.102", "v0.0.102.last"} {
		if _, err := os.Stat(filepath.Join(rel, d)); err == nil {
			t.Errorf("%s survived; nothing refers to it", d)
		}
	}
}

func TestAReadOnlyModuleCacheIsRemoved(t *testing.T) {
	home := isolate(t)
	mod := filepath.Join(home, "cache", "gomodcache", "golang.org", "x", "sys@v0.1.0")
	mkfile(t, filepath.Join(mod, "unix", "a.go"), "package unix\n")
	for _, d := range []string{filepath.Join(mod, "unix"), mod} {
		if err := os.Chmod(d, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = os.Chmod(mod, 0o755)
		_ = os.Chmod(filepath.Join(mod, "unix"), 0o755)
	})
	var b bytes.Buffer
	if err := Remove(&b, track.Env{}, Removable(Survey(track.Env{}), Kind{Cache: true}), false); err != nil {
		t.Fatalf("a 0555 module cache stopped clean: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "cache")); !os.IsNotExist(err) {
		t.Errorf("the cache survived: %v", err)
	}
}

func TestAFailedRemoveSaysWhy(t *testing.T) {
	home := isolate(t)
	t.Setenv("KNO_CACHE", home)
	cmd := Command()
	cmd.SetArgs([]string{"--cache"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := cmd.Execute()
	if err == nil || err.Error() == "" {
		t.Fatalf("a refused removal exits with no reason: %v", err)
	}
}

func TestTheTotalCountsANestedCacheOnce(t *testing.T) {
	home := isolate(t)
	mkfile(t, filepath.Join(home, "cache", "freebsd", "base.txz"), strings.Repeat("x", 1000))
	var b bytes.Buffer
	Report(&b, sorted(Survey(track.Env{})))
	if !strings.Contains(b.String(), "1000 B     total") {
		t.Errorf("the FreeBSD cache inside the build cache counts twice:\n%s", b.String())
	}
}

func TestTheCacheGoesEvenWhenTheEmbeddedRepoIsInsideIt(t *testing.T) {
	home := isolate(t)
	cache := filepath.Join(home, "cache")
	t.Setenv("KNO_CACHE", cache+"/")
	repo := filepath.Join(cache, "repo-0a274bb955228132")
	mkfile(t, filepath.Join(repo, "networks", "n.toml"), "x")
	var b bytes.Buffer
	e := track.Env{Repo: repo}
	if err := Remove(&b, e, Removable(Survey(e), Kind{Cache: true}), false); err != nil {
		t.Fatalf("the materialized repo kept the cache: %v", err)
	}

	src := filepath.Join(home, "src")
	checkout := filepath.Join(src, "kno", "data")
	mkfile(t, filepath.Join(checkout, "networks", "n.toml"), "x")
	t.Setenv("KNO_CACHE", src)
	e = track.Env{Repo: checkout}
	if err := Remove(&b, e, Removable(Survey(e), Kind{Cache: true}), false); err == nil {
		t.Error("a KNO_CACHE above the checkout was removed")
	}
	if _, err := os.Stat(checkout); err != nil {
		t.Fatalf("the checkout is gone: %v", err)
	}
}
