package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/testutil"
)

func bySubject(t *testing.T, r Result) map[string]Finding {
	t.Helper()
	out := map[string]Finding{}
	for _, f := range r.Findings {
		if _, dup := out[f.Subject]; dup {
			t.Fatalf("duplicate finding subject %q", f.Subject)
		}
		out[f.Subject] = f
	}
	return out
}

func failures(r Result) []string {
	var out []string
	for _, f := range r.Findings {
		if f.Status == Fail {
			out = append(out, f.String())
		}
	}
	return out
}

func TestRenderMatchesCommittedConfigs(t *testing.T) {
	repo := testutil.RepoRoot(t)
	r, err := Render(RenderOptions{Repo: repo})
	if err != nil {
		t.Fatal(err)
	}
	if bad := failures(r); len(bad) > 0 {
		t.Fatalf("render check failed:\n%s", strings.Join(bad, "\n"))
	}
	nets, err := networksWithManifest(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(nets) == 0 {
		t.Fatalf("no networks/<name>/editable-configs/manifest.toml found to check")
	}
	found := bySubject(t, r)
	for _, n := range nets {
		f, ok := found["render-drift "+n]
		if !ok {
			t.Fatalf("network %s was not rendered and compared", n)
		}
		if f.Status != Pass || !strings.HasSuffix(f.Detail, "configs match") {
			t.Errorf("network %s: %s", n, f)
		}
	}

	if r.Count(Skip) != 0 || r.Count(Warn) != 0 {
		t.Fatalf("checkout audit must run without warnings or skips: %v", r.Findings)
	}
	for _, subject := range []string{"original namenlos configs", "original namenlos public-keys"} {
		if f, ok := found[subject]; !ok || f.Status != Pass {
			t.Fatalf("original network audit did not pass: %s", subject)
		}
	}
}

func TestRenderRequiresAnExplicitReferenceToExistAndMatch(t *testing.T) {
	repo := testutil.RepoRoot(t)
	ref := filepath.Join(t.TempDir(), "configs")
	for _, state := range []string{"missing", "matching", "changed"} {
		t.Run(state, func(t *testing.T) {
			if state == "matching" {
				if err := fsutil.CopyTreeGuarded(knopath.ConfigsDir(repo, "namenlos"), ref); err != nil {
					t.Fatal(err)
				}
			}
			if state == "changed" {
				if err := os.WriteFile(filepath.Join(ref, "client.toml"), []byte("Changed = true\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			r, err := Render(RenderOptions{Repo: repo, Reference: ref})
			if err != nil {
				t.Fatal(err)
			}
			want := Fail
			if state == "matching" {
				want = Pass
			}
			if f := bySubject(t, r)["parity namenlos vs reference"]; f.Status != want {
				t.Fatalf("reference %s: %s", state, f)
			}
		})
	}
}

func TestRenderReportsABadNetworkInsteadOfExiting(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, "networks", "broken")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.toml"), []byte("this is not toml = = =\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Render(RenderOptions{Repo: repo})
	if err == nil {
		t.Fatal("a manifest that does not decode must come back as an error")
	}
	if !strings.Contains(err.Error(), "manifest.toml") {
		t.Errorf("the error must name what could not be read: %v", err)
	}
}

func TestRenderCatchesAnEditTheHostReadsWithoutARender(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "data")
	if err := fsutil.CopyTreeGuarded(testutil.RepoRoot(t), repo); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string][2]string{
		filepath.Join("services", "wauland-pq-mixserver.conf"): {"UNIT=kp-mix", "UNIT=kp-mix-edited"},
		filepath.Join("machines", "annares.toml"):              {`arch = "ppc64el"`, `arch = "amd64"`},
	} {
		p := filepath.Join(knopath.EditableDir(repo, "namenlos"), name)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		edited := strings.Replace(string(b), edit[0], edit[1], 1)
		if edited == string(b) {
			t.Fatalf("%s: the edit changed nothing; this test covers nothing", name)
		}
		if err := os.WriteFile(p, []byte(edited), 0o644); err != nil {
			t.Fatal(err)
		}
		r, err := Render(RenderOptions{Repo: repo, Networks: []string{"namenlos"}})
		if err != nil {
			t.Fatal(err)
		}
		if f := bySubject(t, r)["render-drift namenlos"]; f.Status != Fail {
			t.Errorf("%s edited without a render: %s", name, f)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCompareDirsReportsEveryKindOfDifference(t *testing.T) {
	base := t.TempDir()
	a := filepath.Join(base, "a")
	b := filepath.Join(base, "b")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(dir, name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(a, "x.toml", "A = 1\n")
	write(b, "x.toml", "A = 2\n")

	write(a, "y.toml", "B = \"  two words  \"\n")
	write(b, "y.toml", "B = \"twowords\"\n")
	write(a, "z.toml", "C = 3\n")
	write(b, "extra.toml", "D = 4\n")

	c, err := compareDirs(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if c.Matched != 1 {
		t.Errorf("matched %d, want 1 (y.toml)", c.Matched)
	}
	want := []string{"DIFF: x.toml", "MISSING in " + b + ": z.toml", "EXTRA in " + b + ": extra.toml"}
	if len(c.Diffs) != len(want) {
		t.Fatalf("diffs %v, want %v", c.Diffs, want)
	}
	for i := range want {
		if c.Diffs[i] != want[i] {
			t.Errorf("diff %d = %q, want %q", i, c.Diffs[i], want[i])
		}
	}
}

func TestRenderDriftComparesTheBytesOfEachConfig(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "data")
	if err := fsutil.CopyTreeGuarded(testutil.RepoRoot(t), repo); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(knopath.ConfigsDir(repo, "namenlos"), "mixy-pq-mixserver.toml")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(b, []byte("# edited by hand\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Render(RenderOptions{Repo: repo, Networks: []string{"namenlos"}})
	if err != nil {
		t.Fatal(err)
	}
	f := bySubject(t, r)["render-drift namenlos"]
	if f.Status != Fail || !strings.Contains(f.Detail, "mixy-pq-mixserver.toml") {
		t.Errorf("a committed config that is not what render writes passed: %s", f)
	}
}
