package adopt

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/build"
	"github.com/katzenpost/kno/internal/track"
)

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@invalid",
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func commit(t *testing.T, dir, path, body, msg string) {
	t.Helper()
	p := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "add", path)
	gitT(t, dir, "commit", "-q", "-m", msg)
}

func fixture(t *testing.T) (track.Env, string) {
	t.Helper()
	kp := t.TempDir()
	gitT(t, kp, "init", "-q", "-b", "main")
	commit(t, kp, "a", "1", "one")
	gitT(t, kp, "tag", "v0.0.107")
	commit(t, kp, "a", "2", "two")
	gitT(t, kp, "tag", "v0.0.108-rc1")
	commit(t, kp, "a", "3", "three")
	gitT(t, kp, "tag", "v0.0.108")

	root := t.TempDir()
	gitT(t, root, "init", "-q", "-b", "main")
	commit(t, root, "data/thirdparty/katzenpost-versions", "# tags\nv0.0.107\n", "versions")
	commit(t, root, "data/thirdparty/pins.env",
		"KATZENPOST_URL=https://example.invalid/katzenpost\nKATZENPOST_REF=v0.0.107\nKATZENPOST_SHA=old\nKATZENPOST_PIN_MODE=tag\n", "pins")
	return track.Env{Repo: filepath.Join(root, "data"), Network: "namenlos", KatzenpostRepo: kp}, root
}

func steps(t *testing.T, calls *[]string, fail string) Steps {
	return Steps{
		Fetch: func(track.Env) error { *calls = append(*calls, "fetch"); return nil },
		Build: func(e track.Env, tag string) (string, error) {
			*calls = append(*calls, "build "+tag)
			if fail == "build" {
				return "", errors.New("podman failed")
			}
			return t.TempDir(), nil
		},
		Record: func(e track.Env, tag, dir string) ([]string, error) {
			*calls = append(*calls, "record "+tag)
			p := filepath.Join(build.CommittedDir(e.Repo, e.Network, tag), "SHA256SUMS")
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return nil, err
			}
			return []string{p}, os.WriteFile(p, []byte("aa  katzenpost.deb\n"), 0o644)
		},
		Sim: func(e track.Env, tag, prev string) error {
			*calls = append(*calls, "sim "+tag+" from "+prev)
			return nil
		},
	}
}

func TestAdoptAddsTheNewestTagWithAMergeCommit(t *testing.T) {
	e, root := fixture(t)
	var calls []string
	tag, err := Run(Options{Env: e, Sim: true, Out: io.Discard}, steps(t, &calls, ""))
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v0.0.108" {
		t.Fatalf("adopted %q, want v0.0.108 and not the rc", tag)
	}
	if got := strings.Join(calls, ", "); got != "fetch, build v0.0.108, record v0.0.108, sim v0.0.108 from v0.0.107" {
		t.Errorf("steps = %s", got)
	}
	if subj := gitT(t, root, "log", "-1", "--format=%s %p"); !strings.HasPrefix(subj, "Merge katzenpost v0.0.108 ") ||
		len(strings.Fields(subj)) != 5 {
		t.Errorf("main ends with %q, want a merge commit with two parents", subj)
	}
	if got := gitT(t, root, "log", "--format=%s", "-3", "katzenpost-v0.0.108"); got != "Record v0.0.108\nAdd katzenpost v0.0.108\npins" {
		t.Errorf("the branch has %q", got)
	}
	b, _ := os.ReadFile(filepath.Join(e.Repo, "thirdparty", "pins.env"))
	sha := gitT(t, e.KatzenpostRepo, "rev-parse", "v0.0.108^{commit}")
	if !strings.Contains(string(b), "KATZENPOST_REF=v0.0.108") || !strings.Contains(string(b), "KATZENPOST_SHA="+sha) {
		t.Errorf("pins.env is\n%s\nwant v0.0.108 at %s", b, sha)
	}
	if v, _ := os.ReadFile(filepath.Join(e.Repo, "thirdparty", "katzenpost-versions")); !strings.HasSuffix(string(v), "v0.0.107\nv0.0.108\n") {
		t.Errorf("katzenpost-versions is %q", v)
	}
	if st := gitT(t, root, "status", "--porcelain"); st != "" {
		t.Errorf("the checkout is not clean after adopt:\n%s", st)
	}

	calls = nil
	tag, err = Run(Options{Env: e, Out: io.Discard}, steps(t, &calls, ""))
	if err != nil || tag != "" {
		t.Errorf("a second run adopted %q (%v), want nothing", tag, err)
	}
}

func TestAFailedBuildLeavesMainAsItWas(t *testing.T) {
	e, root := fixture(t)
	before := gitT(t, root, "rev-parse", "main")
	var calls []string
	_, err := Run(Options{Env: e, Out: io.Discard}, steps(t, &calls, "build"))
	if err == nil || !strings.Contains(err.Error(), "stays on the branch katzenpost-v0.0.108") {
		t.Fatalf("err = %v", err)
	}
	if after := gitT(t, root, "rev-parse", "main"); after != before {
		t.Errorf("main moved from %s to %s after a failed build", before, after)
	}
	if b := gitT(t, root, "rev-parse", "--abbrev-ref", "HEAD"); b != "main" {
		t.Errorf("adopt left the checkout on %s", b)
	}
}

func TestAdoptRefusesADirtyCheckout(t *testing.T) {
	e, _ := fixture(t)
	if err := os.WriteFile(filepath.Join(e.Repo, "thirdparty", "pins.env"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var calls []string
	if _, err := Run(Options{Env: e, Out: io.Discard}, steps(t, &calls, "")); err == nil ||
		!strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("err = %v", err)
	}
}
