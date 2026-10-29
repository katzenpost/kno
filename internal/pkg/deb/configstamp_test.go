package deb

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/knopath"
)

func stampRepo(t *testing.T) (top, repo string, commit func(date string, files map[string]string)) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	top = t.TempDir()
	repo = filepath.Join(top, "data")
	git := func(env []string, args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", top}, args...)...)
		c.Env = append(os.Environ(), append([]string{
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example",
			"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0=/dev/null",
		}, env...)...)
		if b, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	git(nil, "init", "-q", "-b", "main")
	commit = func(date string, files map[string]string) {
		t.Helper()
		for p, body := range files {
			full := filepath.Join(top, p)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		git(nil, "add", "-A")
		git([]string{"GIT_AUTHOR_DATE=" + date, "GIT_COMMITTER_DATE=" + date}, "commit", "-qm", date)
	}
	return top, repo, commit
}

func TestConfigVersionMovesOnlyWithItsOwnConfig(t *testing.T) {
	top, repo, commit := stampRepo(t)
	rel := func(p string) string {
		r, err := filepath.Rel(top, p)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	cfgs := knopath.ConfigsDir(repo, "n")
	deps := knopath.DeploymentsDir(repo, "n")
	commit("2026-10-01T10:00:00Z", map[string]string{
		rel(filepath.Join(cfgs, "a-pq-mixserver.toml")): "a1\n",
		rel(filepath.Join(cfgs, "b-pq-mixserver.toml")): "b1\n",
	})
	a1 := ConfigVersion(repo, "n", "a-pq-mixserver", "v0.0.107", "1")
	b1 := ConfigVersion(repo, "n", "b-pq-mixserver", "v0.0.107", "1")
	if a1 == "0.0.107-1" || b1 == "0.0.107-1" {
		t.Fatalf("the versions carry no stamp: %s %s", a1, b1)
	}

	commit("2026-10-02T10:00:00Z", map[string]string{
		rel(filepath.Join(deps, "a.toml")): "host = \"a\"\n",
		"docs/notes.md":                    "notes\n",
	})
	if a := ConfigVersion(repo, "n", "a-pq-mixserver", "v0.0.107", "1"); a != a1 {
		t.Errorf("a record-only commit moved a's version: %s -> %s", a1, a)
	}
	if b := ConfigVersion(repo, "n", "b-pq-mixserver", "v0.0.107", "1"); b != b1 {
		t.Errorf("a record-only commit moved b's version: %s -> %s", b1, b)
	}

	commit("2026-10-03T10:00:00Z", map[string]string{
		rel(filepath.Join(cfgs, "b-pq-mixserver.toml")): "b2\n",
	})
	if a := ConfigVersion(repo, "n", "a-pq-mixserver", "v0.0.107", "1"); a != a1 {
		t.Errorf("b's config change moved a's version: %s -> %s", a1, a)
	}
	b2 := ConfigVersion(repo, "n", "b-pq-mixserver", "v0.0.107", "1")
	if b2 == b1 {
		t.Fatalf("b's config changed and its version did not: %s", b2)
	}
	if _, err := exec.LookPath("dpkg"); err == nil {
		if err := exec.Command("dpkg", "--compare-versions", b2, "gt", b1).Run(); err != nil {
			t.Errorf("dpkg does not order %s above %s", b2, b1)
		}
	}
}

func ConfigVersion(repo, network, svc, tag, rev string) string {
	return Version(tag, strings.TrimSuffix(rev, "+dirty")) + ConfigStamp(repo, network, svc)
}

func TestARecordedConfigKeepsItsVersionUntilItsInputsChange(t *testing.T) {
	top, repo, commit := stampRepo(t)
	cfg, err := filepath.Rel(top, filepath.Join(knopath.ConfigsDir(repo, "n"), "s.toml"))
	if err != nil {
		t.Fatal(err)
	}
	sums, err := filepath.Rel(top, filepath.Join(knopath.GeneratedIn(knopath.NetworkDir(repo, "n")),
		"releases", "v0.0.107", "nodes", "s", "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	commit("2026-10-15T22:14:08+0200", map[string]string{cfg: "a = 1\n"})
	if got := ConfigStamp(repo, "n", "s"); !strings.HasPrefix(got, "+20261015221408.g") {
		t.Fatalf("before a record: stamp %q, want the stamp of the commit", got)
	}
	commit("2026-10-20T19:22:06+0200", map[string]string{sums: "00  " + knopath.ConfigPkgPrefix +
		"s_0.0.107-1+20260701154849.gdf160a9_amd64.deb\n"})
	if got := ConfigStamp(repo, "n", "s"); got != "+20260701154849.gdf160a9" {
		t.Errorf("a recorded config: stamp %q, want the recorded +20260701154849.gdf160a9", got)
	}
	commit("2026-10-21T10:00:00+0200", map[string]string{cfg: "a = 2\n"})
	if got := ConfigStamp(repo, "n", "s"); !strings.HasPrefix(got, "+20261021100000.g") {
		t.Errorf("a changed config: stamp %q, want the stamp of the change", got)
	}
	commit("2026-10-21T11:00:00+0200", map[string]string{sums: "00  " + knopath.ConfigPkgPrefix +
		"s_0.0.107-1+20260701154849.gdf160a9_amd64.deb\n00  " + knopath.ConfigPkgPrefix +
		"s_0.0.107-1+20261021100000.gabcdef0_amd64.deb\n"})
	if got := ConfigStamp(repo, "n", "s"); got != "+20261021100000.gabcdef0" {
		t.Errorf("a record of two configs of one tag: stamp %q, want the newer +20261021100000.gabcdef0", got)
	}
}

func TestClientStampTracksBothConfigsAndBundlePackaging(t *testing.T) {
	_, repo, commit := stampRepo(t)
	client := "data/networks/n/.generated/configs/client.toml"
	thin := "data/networks/n/.generated/configs/thinclient.toml"
	commit("2026-10-01T10:00:00Z", map[string]string{client: "gateway = \"windfallgw\"\n", thin: "address = \"@katzenpost\"\n"})
	previous := ConfigStamp(repo, "n", "client")
	for i, files := range []map[string]string{
		{client: "gateway = \"firefly\"\n"},
		{thin: "address = \"@other\"\n"},
		{"data/packaging/freebsd/rc.d/kpclientd": "updated\n"},
		{"internal/build/tarball.go": "package build\n"},
	} {
		commit(fmt.Sprintf("2026-10-%02dT10:00:00Z", i+2), files)
		current := ConfigStamp(repo, "n", "client")
		if current == previous {
			t.Fatalf("client stamp did not change for %v", files)
		}
		previous = current
	}
	commit("2026-10-06T10:00:00Z", map[string]string{"README.md": "unrelated\n"})
	if got := ConfigStamp(repo, "n", "client"); got != previous {
		t.Fatalf("unrelated commit changed client stamp: %s -> %s", previous, got)
	}
}

func TestConfigStampTracksDependenciesAcrossTimezones(t *testing.T) {
	_, repo, commit := stampRepo(t)
	service := "data/networks/n/.generated/configs/a-pq-authority.toml"
	client := "data/networks/n/.generated/configs/client.toml"
	commit("2026-10-29T21:45:12+01:00", map[string]string{
		service: "identity = \"first\"\n",
		client:  "gateway = \"first\"\n",
	})
	first := ConfigStamp(repo, "n", "a-pq-authority", "client")
	commit("2026-10-29T21:13:00Z", map[string]string{service: "identity = \"second\"\n"})
	second := ConfigStamp(repo, "n", "a-pq-authority", "client")
	if second == first || second != ConfigStamp(repo, "n", "a-pq-authority") {
		t.Fatalf("newer service commit did not determine the stamp: %s -> %s", first, second)
	}
	commit("2026-10-29T21:13:30Z", map[string]string{
		"data/networks/n/.generated/releases/v1/nodes/a/SHA256SUMS": "abc  namenlos-config-a-pq-authority_1+20261029214512.gabcdef0_all.deb\n",
	})
	if got := ConfigStamp(repo, "n", "a-pq-authority", "client"); got != second {
		t.Fatalf("record commit changed dependency stamp: %s -> %s", second, got)
	}
	commit("2026-10-29T21:14:00Z", map[string]string{client: "gateway = \"second\"\n"})
	third := ConfigStamp(repo, "n", "a-pq-authority", "client")
	if third == second || third != ConfigStamp(repo, "n", "client") {
		t.Fatalf("newer dependency commit did not determine the stamp: %s -> %s", second, third)
	}
}
