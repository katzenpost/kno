package deb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/release"
	"github.com/katzenpost/kno/internal/testutil"
	"time"
)

func sha256File(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestDebIsReproducible(t *testing.T) {
	if _, err := exec.LookPath(compile.Podman()); err != nil {
		t.Skip("podman not available")
	}
	repo := testutil.RepoRoot(t)
	tmp := t.TempDir()
	tag := "v0.0.104"
	svc := "oneeyedman-pq-mixserver"

	t.Setenv("SOURCE_DATE_EPOCH", "1600000000")
	t.Setenv("KNO_BIN", "/nonexistent-kno")
	os.Unsetenv("DEB_BUILD")

	build := func(n string, skew time.Duration) string {
		rel := filepath.Join(tmp, n, tag)
		if err := release.Stage(repo, "namenlos", rel, []string{svc}, nil); err != nil {
			t.Fatalf("Stage: %v", err)
		}

		when := time.Now().Add(skew)
		_ = filepath.Walk(rel, func(p string, fi os.FileInfo, err error) error {
			if err == nil {
				_ = os.Chtimes(p, when, when)
			}
			return nil
		})
		debs, err := PackageConfigs(context.Background(), rel, tag, "", nil)
		if err != nil {
			t.Fatalf("PackageConfigDebs: %v", err)
		}
		if len(debs) != 1 {
			t.Fatalf("expected 1 deb, got %d", len(debs))
		}
		return debs[0]
	}

	a := build("a", 0)
	b := build("b", -73*time.Hour)
	ha, hb := sha256File(t, a), sha256File(t, b)
	if ha != hb {
		t.Errorf("deb is not reproducible:\n  %s %s\n  %s %s",
			filepath.Base(a), ha, filepath.Base(b), hb)
		return
	}
	t.Logf("reproducible: %s == %s (sha256 %s)", filepath.Base(a), filepath.Base(b), ha[:16])
}

func TestSourceDateEpochPrecedence(t *testing.T) {
	repo := testutil.RepoRoot(t)
	t.Setenv("SOURCE_DATE_EPOCH", "12345")
	if got := SourceDateEpoch(repo, "v0.0.104"); got != "12345" {
		t.Errorf("explicit env should win, got %s", got)
	}
	os.Unsetenv("SOURCE_DATE_EPOCH")

	got := SourceDateEpoch(repo, "v-does-not-exist-xyz")
	if got == DefaultEpoch {
		t.Errorf("unknown tag fell back to the constant instead of pins.env")
	}
	if _, err := strconv.Atoi(got); err != nil {
		t.Errorf("fallback is not a timestamp: %q", got)
	}

	if again := SourceDateEpoch(repo, "v-does-not-exist-xyz"); again != got {
		t.Errorf("fallback is not stable: %s then %s", got, again)
	}

	if out, err := exec.Command("git", "-C", repo, "rev-parse", "refs/tags/v0.0.104").Output(); err == nil && len(out) > 0 {
		got := SourceDateEpoch(repo, "v0.0.104")
		if got == DefaultEpoch {
			t.Errorf("existing tag should resolve to its commit date, got the fallback")
		}
		t.Logf("v0.0.104 -> SOURCE_DATE_EPOCH=%s", got)
	}
}

func TestStagedModesDoNotFollowTheUmask(t *testing.T) {
	root := t.TempDir()
	for name, mode := range map[string]os.FileMode{"etc/a.toml": 0o664, "etc/b.pem": 0o600, "DEBIAN/postinst": 0o775, "usr/bin/x": 0o700} {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o775); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(name), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := normalizeModes(root); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]os.FileMode{"etc": 0o755, "etc/a.toml": 0o644, "etc/b.pem": 0o644, "DEBIAN/postinst": 0o755, "usr/bin/x": 0o755} {
		fi, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != want {
			t.Errorf("%s is %04o, want %04o whatever the builder's umask", name, fi.Mode().Perm(), want)
		}
	}
}
