package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/render"
	"github.com/katzenpost/kno/internal/testutil"
)

func TestRenderRequiresOriginalNamenlosBaselines(t *testing.T) {
	for _, kind := range []string{"configs", "public-keys"} {
		for _, damage := range []string{"missing", "empty", "malformed", "incomplete"} {
			t.Run(kind+"/"+damage, func(t *testing.T) {
				repo := t.TempDir()
				if err := fsutil.CopyTreeGuarded(testutil.RepoRoot(t), repo); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(repo, "testdata", "namenlos-"+kind+".sha256")
				b, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				switch damage {
				case "missing":
					err = os.Remove(path)
				case "empty":
					err = os.WriteFile(path, nil, 0o644)
				case "malformed":
					err = os.WriteFile(path, []byte("not a digest\n"), 0o644)
				case "incomplete":
					_, rest, _ := strings.Cut(string(b), "\n")
					err = os.WriteFile(path, []byte(rest), 0o644)
				}
				if err != nil {
					t.Fatal(err)
				}
				r, err := Render(RenderOptions{Repo: repo})
				if err != nil {
					t.Fatal(err)
				}
				if f := bySubject(t, r)["original namenlos "+kind]; f.Status != Fail {
					t.Fatalf("%s baseline %s did not fail: %s", kind, damage, f)
				}
			})
		}
	}
}

func TestRenderRejectsRegeneratedProtocolChangesAgainstOriginalNamenlos(t *testing.T) {
	repo := t.TempDir()
	if err := fsutil.CopyTreeGuarded(testutil.RepoRoot(t), repo); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(knopath.EditableDir(repo, "namenlos"), "nodes", "respectmy-authority.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.ReplaceAll(string(b), "65.109.148.215:1984", "65.109.148.215:1985")
	if changed == string(b) {
		t.Fatal("protocol edit changed nothing")
	}
	if err := os.WriteFile(path, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := render.Generate(knopath.NetworkDir(repo, "namenlos"), ""); err != nil {
		t.Fatal(err)
	}
	r, err := Render(RenderOptions{Repo: repo})
	if err != nil {
		t.Fatal(err)
	}
	found := bySubject(t, r)
	if f := found["render-drift namenlos"]; f.Status != Pass {
		t.Fatalf("regenerated configs should agree: %s", f)
	}
	if f := found["original namenlos configs"]; f.Status != Fail || !strings.Contains(f.Detail, "respectmy-pq-authority.toml") {
		t.Fatalf("protocol edit escaped original config audit: %s", f)
	}
}

func TestRenderRejectsOriginalPublicKeyChangesInBothTrees(t *testing.T) {
	for _, dir := range []string{"editable-configs/keys", ".generated/keys"} {
		t.Run(dir, func(t *testing.T) {
			repo := t.TempDir()
			if err := fsutil.CopyTreeGuarded(testutil.RepoRoot(t), repo); err != nil {
				t.Fatal(err)
			}
			sums, err := originalDigests(filepath.Join(repo, "testdata", "namenlos-public-keys.sha256"))
			if err != nil {
				t.Fatal(err)
			}
			var key string
			for name := range sums {
				if strings.Contains(name, "wauland2") {
					key = name
					break
				}
			}
			if key == "" {
				t.Fatal("no original wauland2 key")
			}
			path := filepath.Join(knopath.NetworkDir(repo, "namenlos"), dir, key)
			if err := os.WriteFile(path, []byte("changed public key\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			r, err := Render(RenderOptions{Repo: repo})
			if err != nil {
				t.Fatal(err)
			}
			if f := bySubject(t, r)["original namenlos public-keys"]; f.Status != Fail || !strings.Contains(f.Detail, path) {
				t.Fatalf("key edit escaped original key audit: %s", f)
			}
		})
	}
}
