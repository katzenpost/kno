package track

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func needTools(t *testing.T, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, err := exec.LookPath(n); err != nil {
			t.Skipf("%s not available", n)
		}
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "core.hooksPath=/dev/null", "-c", "user.name=kno-test", "-c", "user.email=kno@test"}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func initRepo(t *testing.T, dir string, tags ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q")
	git(t, dir, "commit", "--allow-empty", "-q", "-m", "root")
	for _, tag := range tags {
		git(t, dir, "tag", tag)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const knoTOML = `network = "testnet"
nodes = ["alpha"]

[update]
policy = "apply"
require_signed_tag = %v

[push]
mode = "full"

[hosts."testhost"]
scheduler = "none"
services = ["alpha-pq-authority"]
`

func networkRepo(t *testing.T, dir string, tags ...string) {
	t.Helper()
	initRepo(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, "networks"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeVersions(t, dir, strings.Join(tags, "\n")+"\n")
}

type fixture struct {
	home, repo, kp, state, toml string
}

func newFixture(t *testing.T, signedTags bool) fixture {
	t.Helper()
	home := t.TempDir()
	f := fixture{
		home:  home,
		repo:  filepath.Join(home, "network-organizer"),
		kp:    filepath.Join(home, "katzenpost"),
		state: filepath.Join(home, "state"),
		toml:  filepath.Join(home, "kno.toml"),
	}
	write(t, f.toml, fmt.Sprintf(knoTOML, signedTags))
	if err := os.MkdirAll(f.state, 0o755); err != nil {
		t.Fatal(err)
	}

	for k, v := range map[string]string{
		"HOME":              f.home,
		"KNO_HOME":          f.home,
		"KNO_REPO":          f.repo,
		"KATZENPOST_REPO":   f.kp,
		"UPDATER_STATE_DIR": f.state,
		"KNO_CONFIG":        f.toml,
		"KNO_HOST":          "testhost",
		"KNO_GIT_FETCH":     "0",
		"KNO_CLONE_KP":      "0",
		"KNO_CLONE":         "0",
		"KNO_TRACK_FETCH":   "1",
		"GIT_CONFIG_GLOBAL": os.DevNull,
		"GIT_CONFIG_SYSTEM": os.DevNull,

		"SERVICES":          "",
		"SERVICES_DIR":      "",
		"KNO_NETWORK":       "",
		"VERIFY_TAG":        "",
		"KNO_UPDATE_POLICY": "",
		"KNO_PUSH_MODE":     "",
		"QUARANTINE_FILE":   "",
		"CURRENT_FILE":      "",
		"KATZENPOST_URL":    "",
	} {
		t.Setenv(k, v)
	}
	return f
}

var logLine = regexp.MustCompile(`^\S+ (kno\[[a-z]+\]: .*)$`)

func logFacts(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if m := logLine.FindStringSubmatch(ln); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}

func runGo(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var o, e bytes.Buffer
	code = Run(&o, &e, args)
	return o.String(), e.String(), code
}

func TestTrackPicksTheTarget(t *testing.T) {
	needTools(t, "git")

	cases := []struct {
		name    string
		signed  bool
		netTags []string
		kpTags  []string
		setup   func(t *testing.T, f fixture)
		want    string
		wantRC  int
	}{{
		name:    "newest tag in both repos wins",
		netTags: []string{"v0.0.1", "v0.0.2", "v0.0.3", "v0.0.10", "vnext", "release-1"},
		kpTags:  []string{"v0.0.1", "v0.0.2", "v0.0.10"},
		want:    "v0.0.10\n",
	}, {
		name:    "tag missing from katzenpost is skipped",
		netTags: []string{"v0.0.1", "v0.0.2", "v0.0.3"},
		kpTags:  []string{"v0.0.1"},
		want:    "v0.0.1\n",
	}, {
		name:    "quarantine skips to the next version",
		netTags: []string{"v0.0.1", "v0.0.2", "v0.0.10"},
		kpTags:  []string{"v0.0.1", "v0.0.2", "v0.0.10"},
		setup: func(t *testing.T, f fixture) {
			write(t, filepath.Join(f.state, "current"), "v0.0.1\n")
			write(t, filepath.Join(f.state, "quarantine"), "v0.0.10\n")
		},
		want: "v0.0.2\n",
	}, {
		name:    "a quarantined newest tag with nothing installed is no target",
		netTags: []string{"v0.0.1", "v0.0.2", "v0.0.10"},
		kpTags:  []string{"v0.0.1", "v0.0.2", "v0.0.10"},
		setup: func(t *testing.T, f fixture) {
			write(t, filepath.Join(f.state, "quarantine"), "v0.0.10\n")
		},
		wantRC: 1,
	}, {
		name:    "already at the target",
		netTags: []string{"v0.0.1", "v0.0.2"},
		kpTags:  []string{"v0.0.1", "v0.0.2"},
		setup: func(t *testing.T, f fixture) {
			write(t, filepath.Join(f.state, "current"), "v0.0.2\n")
		},
		wantRC: 3,
	}, {
		name:    "no shared tag",
		netTags: []string{"v0.0.1"},
		kpTags:  []string{"v9.9.9"},
		wantRC:  1,
	}, {
		name:    "no katzenpost clone at all",
		netTags: []string{"v0.0.1"},
		setup: func(t *testing.T, f fixture) {
			if err := os.RemoveAll(f.kp); err != nil {
				t.Fatal(err)
			}
		},
		wantRC: 1,
	}, {
		name:    "signed tags required, none are signed",
		signed:  true,
		netTags: []string{"v0.0.1", "v0.0.2"},
		kpTags:  []string{"v0.0.1", "v0.0.2"},
		wantRC:  1,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.signed)
			networkRepo(t, f.repo, tc.netTags...)
			initRepo(t, f.kp, tc.kpTags...)
			if tc.setup != nil {
				tc.setup(t, f)
			}

			out, errOut, rc := runGo(t)
			if out != tc.want || rc != tc.wantRC {
				t.Errorf("got %q rc=%d, want %q rc=%d\n%s", out, rc, tc.want, tc.wantRC, errOut)
			}
			if len(logFacts(errOut)) == 0 {
				t.Errorf("track logged nothing:\n%s", errOut)
			}
		})
	}
}

func TestTrackHelpGoesToStderr(t *testing.T) {
	needTools(t, "git")
	f := newFixture(t, false)
	networkRepo(t, f.repo, "v0.0.1")
	initRepo(t, f.kp, "v0.0.1")

	goOut, goErr, goRC := runGo(t, "--help")
	if goRC != 0 {
		t.Errorf("--help exit status %d, want 0", goRC)
	}
	if goOut != "" {
		t.Errorf("--help wrote to stdout: %q", goOut)
	}
	if !strings.Contains(goErr, "print the target tag") {
		t.Errorf("go --help text missing from stderr: %q", goErr)
	}
}

func TestALegacyConfThatVerifiesTagsRefusesUnsignedOnes(t *testing.T) {
	needTools(t, "git")
	if _, err := os.Stat("/etc/katzenpost/kno.toml"); err == nil {
		t.Skip("/etc/katzenpost/kno.toml exists here, and kno reads it before kno.conf")
	}
	f := newFixture(t, false)
	networkRepo(t, f.repo, "v0.0.1", "v0.0.2")
	initRepo(t, f.kp, "v0.0.1", "v0.0.2")
	t.Setenv("KNO_CONFIG", "")
	conf := filepath.Join(f.home, "kno.conf")
	write(t, conf, "SERVICES=\"alpha-pq-authority\"\nVERIFY_TAG=1\n")
	t.Setenv("KNO_UPDATER_CONF", conf)

	out, errOut, rc := runGo(t)
	if out != "" || rc != 1 {
		t.Errorf("VERIFY_TAG=1 with unsigned tags: got %q rc=%d, want no target and rc=1\n%s", out, rc, errOut)
	}
}

func TestLegacyConfParse(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "kno.conf")
	write(t, conf, "# comment\n\nSERVICES=\"a-pq-authority b-pq-mixserver\"\nVERIFY_TAG=1\nKNO_NETWORK=other\n")
	e := Env{LegacyConf: conf}
	if err := e.applyLegacyConf(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(e.Services, " "); got != "a-pq-authority b-pq-mixserver" {
		t.Errorf("SERVICES = %q", got)
	}
	if !e.VerifyTag || e.Network != "other" {
		t.Errorf("VERIFY_TAG = %v, KNO_NETWORK = %q", e.VerifyTag, e.Network)
	}
}

func TestKPHasTagChecksTheUpstreamPartOfARevisionTag(t *testing.T) {
	kp := t.TempDir()
	initRepo(t, kp, "v0.0.104")

	e := Env{KatzenpostRepo: kp}

	if !e.KPHasTag("v0.0.104") {
		t.Fatal("the bare tag is not found")
	}
	if !e.KPHasTag("v0.0.104-141") {
		t.Error("a revision tag was not matched to its upstream part," +
			" so a config-only release can never be targeted")
	}
	if e.KPHasTag("v0.0.105-1") {
		t.Error("a revision tag matched an upstream katzenpost does not have")
	}

	if e.KPHasTag("v0.0.104-rc1") {
		t.Error("a pre-release suffix was treated as a revision")
	}
}
