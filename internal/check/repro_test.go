package check

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/testutil"
)

func TestReproStepsArePlannedBeforeAnythingRuns(t *testing.T) {
	repo := testutil.RepoRoot(t)
	steps := ReproSteps(ReproOptions{Repo: repo, Work: "/w"})
	if len(steps) != 4 {
		t.Fatalf("want 4 legs, got %d: %v", len(steps), steps)
	}
	want := []string{"kno binary", "vendored toolchain", "deb", "freebsd pkg"}
	for i, n := range want {
		if steps[i].Name != n {
			t.Errorf("leg %d is %q, want %q", i, steps[i].Name, n)
		}
	}
	wantArgv := "go build -trimpath -buildvcs=false -o /w/kno-a ./cmd/kno"
	if got := strings.Join(steps[0].Argv, " "); got != wantArgv {
		t.Errorf("kno binary argv:\n got %s\nwant %s", got, wantArgv)
	}

	if len(steps[1].Argv) != 0 {
		t.Errorf("the toolchain leg does not shell out, so it has no argv: %v", steps[1].Argv)
	}
	if !steps[1].NeedsNetwork {
		t.Error("the toolchain leg fetches the pinned sources and must say it needs a network")
	}
	if !steps[2].NeedsPodman {
		t.Error("the deb leg must say it needs podman")
	}
	if steps[3].NeedsPodman || steps[3].NeedsNetwork {
		t.Error("the freebsd leg is pure Go and needs nothing")
	}
	for _, i := range []int{0, 1, 2} {
		if !steps[i].Required {
			t.Errorf("leg %q must be required by default; a laptop says so with --allow-skip", steps[i].Name)
		}
	}
	for _, s := range ReproSteps(ReproOptions{Repo: repo, Work: "/w", AllowSkip: true}) {
		if s.Required {
			t.Errorf("--allow-skip must leave no leg required, %q still is", s.Name)
		}
	}
}

func TestReproRunsExactlyWhatItPlanned(t *testing.T) {
	work := t.TempDir()
	bin := t.TempDir()
	record := filepath.Join(work, "argv")
	stub := "#!/bin/sh\nprintf 'go %s\\n' \"$*\" >> " + record + "\n" +
		"while [ $# -gt 0 ]; do if [ \"$1\" = -o ]; then : > \"$2\"; fi; shift; done\n"
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	var r Result
	reproKno(context.Background(), &r, t.TempDir(), work, true)

	b, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("the leg did not run go at all: %v", err)
	}
	ran := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(ran) != 2 {
		t.Fatalf("want two builds, got %v", ran)
	}
	planned := strings.Join(ReproSteps(ReproOptions{Work: work})[0].Argv, " ")
	if ran[0] != planned {
		t.Errorf("the plan and the build disagree:\n ran %s\nplan %s", ran[0], planned)
	}

	if a, b := strings.Replace(ran[0], "kno-a", "kno-b", 1), ran[1]; a != b {
		t.Errorf("the two builds differ by more than the output path:\n%s\n%s", a, b)
	}
}

func TestReproBuildsKnoWithAFreshCacheEachTime(t *testing.T) {
	work := t.TempDir()
	bin := t.TempDir()
	record := filepath.Join(work, "gocache")
	stub := "#!/bin/sh\nprintf '%s\\n' \"$GOCACHE\" >> " + record + "\n" +
		"while [ $# -gt 0 ]; do if [ \"$1\" = -o ]; then : > \"$2\"; fi; shift; done\n"
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GOCACHE", filepath.Join(t.TempDir(), "shared"))

	var r Result
	reproKno(context.Background(), &r, t.TempDir(), work, true)

	b, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	caches := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(caches) != 2 || caches[0] == caches[1] {
		t.Fatalf("the two builds used the caches %q; want two different ones", caches)
	}
	for _, c := range caches {
		if !strings.HasPrefix(c, work+string(filepath.Separator)) {
			t.Errorf("build cache %q is not a new one under %s", c, work)
		}
	}
}

func TestReproToolchainComparesBothBuilds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		stamped bool
		want    Status
	}{
		{"deterministic", false, Pass},
		{"stamped", true, Fail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := 0
			restore := buildToolchain
			t.Cleanup(func() { buildToolchain = restore })
			buildToolchain = func(_, work, _ string) (string, error) {
				n++
				bin := filepath.Join(work, "bin")
				if err := os.MkdirAll(bin, 0o755); err != nil {
					return "", err
				}
				body := "genconfig"
				if tc.stamped {
					body = fmt.Sprintf("genconfig build %d", n)
				}
				if err := os.WriteFile(filepath.Join(bin, "genconfig"), []byte(body), 0o755); err != nil {
					return "", err
				}
				if err := os.WriteFile(filepath.Join(bin, "kpconfig"), []byte("kpconfig"), 0o755); err != nil {
					return "", err
				}
				return bin, nil
			}
			var r Result
			reproToolchain(&r, t.TempDir(), t.TempDir(), true, true)
			if n != 2 {
				t.Fatalf("the leg built %d time(s), want 2", n)
			}
			found := bySubject(t, r)
			if f := found["toolchain genconfig"]; f.Status != tc.want {
				t.Errorf("toolchain genconfig: want %s, got %s", tc.want, f)
			}
			if f := found["toolchain kpconfig"]; f.Status != Pass {
				t.Errorf("toolchain kpconfig: %s", f)
			}
		})
	}
}

func TestReproFpkgReportsOnlyFindings(t *testing.T) {
	var r Result
	var ferr error
	leaked, err := captureAll("", func() { ferr = reproFpkg(&r, t.TempDir(), "1700000000") })
	if err != nil {
		t.Fatal(err)
	}
	if ferr != nil {
		t.Fatal(ferr)
	}
	if r.Failed() {
		t.Fatalf("%v", r.Findings)
	}
	if s := strings.TrimSpace(leaked); s != "" {
		t.Errorf("the leg wrote outside the report:\n%s", s)
	}
}

func TestCaptureRestoresTheStreamsWhenFnPanics(t *testing.T) {
	stdout, stderr := os.Stdout, os.Stderr
	func() {
		defer func() {
			if recover() == nil {
				t.Error("capture swallowed the panic")
			}
		}()
		_, _ = captureAll("", func() {
			fmt.Println("written before the panic")
			panic("boom")
		})
	}()
	if os.Stdout != stdout || os.Stderr != stderr {
		t.Fatal("capture left the process streams pointed at a closed pipe")
	}
}

func TestReproFailsOnAMissingPrerequisite(t *testing.T) {
	t.Setenv("PODMAN", filepath.Join(t.TempDir(), "no-podman-here"))
	var r Result
	reproDeb(context.Background(), &r, t.TempDir(), "0", true)
	reproToolchain(&r, t.TempDir(), t.TempDir(), false, true)
	if r.Count(Fail) != 2 {
		t.Fatalf("both unmet legs must fail by default: %v", r.Findings)
	}
	var s Result
	reproDeb(context.Background(), &s, t.TempDir(), "0", false)
	reproToolchain(&s, t.TempDir(), t.TempDir(), false, false)
	if s.Count(Skip) != 2 || s.Failed() {
		t.Fatalf("--allow-skip must turn them back into skips: %v", s.Findings)
	}
}

func TestReproBuildsIdenticalArtifacts(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the kno binary twice")
	}
	repo := testutil.RepoRoot(t)

	r, err := Repro(context.Background(), ReproOptions{Repo: repo, Work: t.TempDir(), AllowSkip: true})
	if err != nil {
		t.Fatal(err)
	}
	if bad := failures(r); len(bad) > 0 {
		t.Fatalf("not reproducible:\n%s", strings.Join(bad, "\n"))
	}
	found := bySubject(t, r)

	if f := found["freebsd pkg"]; f.Status != Pass {
		t.Errorf("freebsd pkg: %s", f)
	}
	if f := found["kno binary"]; f.Status != Pass {
		t.Errorf("kno binary: %s", f)
	}

	if f := found["vendored toolchain"]; f.Status != Skip {
		t.Errorf("vendored toolchain: %s", f)
	}
	f := found["deb"]
	if _, err := exec.LookPath(compile.Podman()); err != nil {
		if f.Status != Skip {
			t.Errorf("without podman the deb leg must skip, got %s", f)
		}
		return
	}
	if f.Status != Pass {
		t.Errorf("deb: %s", f)
	}
}
