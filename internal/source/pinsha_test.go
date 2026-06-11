package source

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitP(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.hooksPath",
		"GIT_CONFIG_VALUE_0=/dev/null",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func upstreamRepo(t *testing.T) (dir, commit string) {
	t.Helper()
	dir = t.TempDir()
	gitP(t, dir, "init", "-q", "-b", "main", ".")
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitP(t, dir, "add", "go.mod")
	gitP(t, dir, "commit", "-qm", "one")
	gitP(t, dir, "tag", "-a", "v1.0.0", "-m", "release")
	return dir, gitP(t, dir, "rev-parse", "v1.0.0^{commit}")
}

func TestResolveSHAPeelsAnnotatedTag(t *testing.T) {
	dir, want := upstreamRepo(t)

	if obj := gitP(t, dir, "rev-parse", "v1.0.0"); obj == want {
		t.Fatal("test is not exercising an annotated tag")
	}
	got, err := ResolveSHA("v1.0.0", dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("ResolveSHA = %s, want the commit %s", got, want)
	}
}

func TestResolveSHARefusesUnsafeRef(t *testing.T) {
	dir, _ := upstreamRepo(t)
	if _, err := ResolveSHA("--upload-pack=touch /tmp/x", dir, dir); err == nil {
		t.Fatal("a ref git would read as an option must be refused")
	}
}

func TestFetchComponentRefusesMovedTag(t *testing.T) {
	up, first := upstreamRepo(t)

	work := t.TempDir()
	if err := fetchComponent(work, "c", up, "v1.0.0", first, up); err != nil {
		t.Fatalf("fetch at the pinned commit: %v", err)
	}

	if err := os.WriteFile(filepath.Join(up, "evil"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitP(t, up, "add", "evil")
	gitP(t, up, "commit", "-qm", "two")
	gitP(t, up, "tag", "-d", "v1.0.0")
	gitP(t, up, "tag", "-a", "v1.0.0", "-m", "release")
	moved := gitP(t, up, "rev-parse", "v1.0.0^{commit}")
	if moved == first {
		t.Fatal("the tag did not move")
	}

	err := fetchComponent(t.TempDir(), "c", up, "v1.0.0", first, up)
	if err == nil {
		t.Fatal("a tag that moved off the pinned commit must be refused")
	}
	for _, want := range []string{first, moved, "the pin says"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %q, got: %v", want, err)
		}
	}
}

func TestFetchComponentWithoutPinStillBuilds(t *testing.T) {
	up, _ := upstreamRepo(t)

	if err := fetchComponent(t.TempDir(), "c", up, "v1.0.0", "", up); err != nil {
		t.Fatalf("an empty pin must not block a build: %v", err)
	}
}

func TestSetPinsInsertsSHANextToRef(t *testing.T) {
	body := "GO_VERSION=1.26.2\nKATZENPOST_REF=v0.0.1\nKATZENPOST_SRC=/s\n"
	out, err := setPins(body, map[string]string{
		"KATZENPOST_REF": "v0.0.2", "KATZENPOST_SHA": "abc", "KATZENPOST_PIN_MODE": "tag",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"KATZENPOST_REF=v0.0.2", "KATZENPOST_SHA=abc",
		"KATZENPOST_PIN_MODE=tag", "KATZENPOST_SRC=/s"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}

	if strings.Count(out, "KATZENPOST_SHA=") != 1 {
		t.Errorf("expected exactly one sha line:\n%s", out)
	}
}

func TestShippedPinsCarrySHAs(t *testing.T) {
	pins, err := ParsePins(filepath.Join("..", "..", "data", "thirdparty", "pins.env"))
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"KATZENPOST_SHA", "HPQC_SHA"} {
		v := pins[k]
		if len(v) != 40 {
			t.Errorf("%s = %q, want a 40-character commit sha", k, v)
		}
		if strings.Trim(v, "0123456789abcdef") != "" {
			t.Errorf("%s = %q is not hex", k, v)
		}
	}
}
