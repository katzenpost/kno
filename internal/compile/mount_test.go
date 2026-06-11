package compile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

func TestMountAbsolutizes(t *testing.T) {
	got, err := Mount("dist/demo/.src", "/work:rw")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "/") {
		t.Errorf("relative source not absolutized: %s", got)
	}
	if !strings.HasSuffix(got, ":/work:rw") {
		t.Errorf("spec lost: %s", got)
	}
	abs, _ := filepath.Abs("dist/demo/.src")
	if got != abs+":/work:rw" {
		t.Errorf("got %s, want %s", got, abs+":/work:rw")
	}
}

func TestMountNamesTheRealDirectory(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "tmp")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Mount(link, "/work:rw")
	if err != nil {
		t.Fatal(err)
	}
	if got != want+":/work:rw" {
		t.Errorf("got %s, want %s: podman machine on macOS shares /private, not the /tmp and /var/tmp symlinks to it",
			got, want+":/work:rw")
	}
}

func TestEveryBindMountIsAbsolutized(t *testing.T) {
	root := testutil.ModuleRoot(t)
	skipDir := map[string]bool{".git": true, "dist": true, "thirdparty": true, "networks": true}
	allow := map[string]bool{filepath.Join("internal", "activate", "activate.go"): true}
	scanned := 0
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if fi.IsDir() {
			if skipDir[rel] || (rel != "." && strings.HasPrefix(fi.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		scanned++
		if allow[rel] {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			if !strings.Contains(line, `"-v"`) || strings.Contains(line, "Mount(") {
				continue
			}
			t.Errorf("%s:%d builds a bind mount without Mount/MustMount:\n  %s",
				rel, i+1, strings.TrimSpace(line))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if scanned < 10 {
		t.Fatalf("scanned only %d files under %s; the walk is not reaching the source", scanned, root)
	}
}
