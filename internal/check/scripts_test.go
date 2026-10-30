package check

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

func stubKno(t *testing.T, body string) string {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "kno"), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func makefile(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testutil.ModuleRoot(t), "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMakeUpgradeKeepsTheExitCodeOfKnoDoctor(t *testing.T) {
	line := ""
	for _, l := range strings.Split(makefile(t), "\n") {
		if strings.Contains(l, "kno doctor") && strings.Contains(l, ".kno-iterate-rc") {
			line = l
		}
	}
	if line == "" {
		t.Fatal("the upgrade target writes no .kno-iterate-rc after kno doctor")
	}
	dir := t.TempDir()
	sh := strings.TrimPrefix(strings.TrimSpace(line), "@")
	sh = strings.ReplaceAll(sh, "$$", "$")
	sh = strings.ReplaceAll(sh, "$(ITERLOG)", filepath.Join(dir, "log"))
	cmd := exec.Command("sh", "-c", sh)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+stubKno(t, "exit 3\n")+string(os.PathListSeparator)+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", sh, err, out)
	}
	rc, err := os.ReadFile(filepath.Join(dir, ".kno-iterate-rc"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(rc)); got != "3" {
		t.Errorf(".kno-iterate-rc = %q after kno doctor exited 3", got)
	}
}

var reKnoCall = regexp.MustCompile(`(?:\$\(KNO\)|@kno|["'] kno) ([a-z][a-z-]*)`)

func TestEveryKnoCommandTheMakefileRunsExists(t *testing.T) {
	kno := filepath.Join(t.TempDir(), "kno")
	build := exec.Command("go", "build", "-o", kno, "./cmd/kno")
	build.Dir = testutil.ModuleRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/kno: %v\n%s", err, out)
	}
	seen := map[string]bool{}
	for _, m := range reKnoCall.FindAllStringSubmatch(makefile(t), -1) {
		sub := m[1]
		if seen[sub] {
			continue
		}
		seen[sub] = true
		if out, err := exec.Command(kno, sub, "--help").CombinedOutput(); err != nil {
			t.Errorf("the Makefile runs 'kno %s', which kno does not have: %v\n%s", sub, err, out)
		}
	}
	if len(seen) < 3 {
		t.Fatalf("found only %v in the Makefile; the pattern is wrong", seen)
	}
}

func gitIn(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestMakeHooksWorksInAWorktreeAndKeepsAGlobalHooksPath(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("no make on this host")
	}
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "Makefile"), []byte(makefile(t)), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, env, "init", "-q", "-b", "main")
	gitIn(t, repo, env, "add", "Makefile")
	gitIn(t, repo, env, "commit", "-q", "--no-verify", "-m", "base")
	wt := filepath.Join(base, "wt")
	gitIn(t, repo, env, "worktree", "add", "-q", wt)

	run := func(dir string) error {
		cmd := exec.Command("make", "-s", "hooks")
		cmd.Dir = dir
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Logf("make hooks in %s: %v\n%s", dir, err, out)
		}
		return err
	}
	if err := run(wt); err != nil {
		t.Fatal("make hooks failed in a worktree")
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "hooks", "pre-commit")); err != nil {
		t.Errorf("make hooks in a worktree installed no hook that git runs: %v", err)
	}

	global := filepath.Join(base, "global-hooks")
	gitIn(t, repo, env, "config", "core.hooksPath", global)
	if err := run(repo); err == nil {
		t.Error("make hooks passed, but git runs the hooks in core.hooksPath and not this one")
	}
	if _, err := os.Stat(filepath.Join(global, "pre-commit")); err == nil {
		t.Error("make hooks wrote over the hooks in core.hooksPath")
	}
}

func copyScript(t *testing.T, dst, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testutil.ModuleRoot(t), "scripts", name))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dst, "scripts", name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGatesReportAGoFileThatGofmtCannotRead(t *testing.T) {
	repo := t.TempDir()
	gates := copyScript(t, repo, "gates.sh")
	if err := os.WriteFile(filepath.Join(repo, "bad.go"), []byte("package x\nfunc {\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GATES_SKIP_TESTS=1", "GATES_SKIP_REPO=1", "GATES_SKIP_SIM=1")
	gitIn(t, repo, env, "init", "-q", "-b", "main")
	gitIn(t, repo, env, "add", "bad.go", "scripts/gates.sh")
	cmd := exec.Command("sh", gates)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the gates passed with an unparsable Go file:\n%s", out)
	}
	for _, want := range []string{"FAIL  gofmt", "bad.go", "==> gates FAILED"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("the gates output does not contain %q:\n%s", want, out)
		}
	}
}

const mutationKno = `cmd=$2
shift 2
while [ $# -gt 0 ]; do
	case "$1" in --workdir) w=$2; shift ;; esac
	shift
done
case "$cmd" in
configs) mkdir -p "$w/configs"; printf '[Server]\n  Identifier = "mixy"\n' >"$w/configs/mixy-pq-mixserver.toml" ;;
up) cat "$UPLOG"; exit 1 ;;
esac
`

func TestSimMutationNeedsTheServiceOnARefusedLine(t *testing.T) {
	script := copyScript(t, t.TempDir(), "sim-mutation.sh")
	kno := filepath.Join(stubKno(t, mutationKno), "kno")
	for _, c := range []struct {
		name, log string
		pass      bool
	}{
		{"refused by its daemon", "t kno[sim]: ERROR: 1 config(s) refused by their own daemon:\n" +
			"  mixy-pq-mixserver: toml: unknown field NoSuchField\n", true},
		{"exited after launch", "t kno[sim]: ERROR: 2 daemon(s) exited after launch:\n" +
			"  reed-pq-mixserver\n    panic: x\n  mixy-pq-mixserver\n    unknown field\n", true},
		{"named, but not refused", "t kno[sim]: preparing mixy-pq-mixserver\n" +
			"t kno[sim]: ERROR: network create failed\n", false},
		{"another service refused", "t kno[sim]: ERROR: 1 config(s) refused by their own daemon:\n" +
			"  reed-pq-mixserver: refused\nt kno[sim]: mixy-pq-mixserver was fine\n", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			log := filepath.Join(t.TempDir(), "up.log")
			if err := os.WriteFile(log, []byte(c.log), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("sh", script)
			cmd.Env = append(os.Environ(), "KNO="+kno, "UPLOG="+log, "TMPDIR="+t.TempDir())
			out, err := cmd.CombinedOutput()
			if (err == nil) != c.pass {
				t.Errorf("sim-mutation exited with %v, want pass=%v:\n%s", err, c.pass, out)
			}
		})
	}
}

var (
	reMarkerList = regexp.MustCompile(`(?s)for m in (.*?); do`)
	reExpFile    = regexp.MustCompile(`\$SELF/(kno-freebsd-[a-z]+\.exp)`)
)

func TestTheFreeBSDDriversCannotPassOnTheirOwnEcho(t *testing.T) {
	dir := filepath.Join(testutil.ModuleRoot(t), "test")
	drivers, err := filepath.Glob(filepath.Join(dir, "kno-freebsd-*"))
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, d := range drivers {
		if strings.HasSuffix(d, ".exp") {
			continue
		}
		b, err := os.ReadFile(d)
		if err != nil {
			t.Fatal(err)
		}
		body := string(b)
		if strings.Contains(body, `:-0}"`) && strings.Contains(body, "kill") {
			t.Errorf("%s can kill pid 0, the whole process group", filepath.Base(d))
		}
		exp := reExpFile.FindStringSubmatch(body)
		lists := reMarkerList.FindAllStringSubmatch(body, -1)
		if exp == nil || len(lists) == 0 {
			continue
		}
		eb, err := os.ReadFile(filepath.Join(dir, exp[1]))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(body, `grep -q "${m}=0"`) {
			t.Errorf("%s finds a marker anywhere on a line, also in the echo of the command", filepath.Base(d))
		}
		for _, l := range lists {
			for _, m := range strings.Fields(strings.ReplaceAll(l[1], "\\", " ")) {
				checked++
				if strings.Contains(string(eb), m+"=0") {
					t.Errorf("%s writes %s=0 into its command, so the echo of the command passes it",
						exp[1], m)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("found no pass marker in any driver; the patterns are wrong")
	}
}

func TestGatesKeepFixtureFailuresOutOfTheRealSummary(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(testutil.ModuleRoot(t), "scripts", "gates.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(b)
	start := strings.Index(script, "if limit \"go test\" env ")
	if start < 0 {
		t.Fatal("Go test gate not found")
	}
	end := strings.Index(script[start:], "go test ./...")
	if end < 0 {
		t.Fatal("Go test invocation not found")
	}
	invocation := strings.TrimPrefix(script[start:start+end+len("go test ./...")], "if ")
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	stub := "#!/bin/sh\n[ \"$1\" = test ] || exit 1\nprintf visited >\"$TEST_VISITED\"\nif [ -n \"${GITHUB_STEP_SUMMARY:-}\" ]; then printf 'FAIL fixture\\n' >>\"$GITHUB_STEP_SUMMARY\"; fi\n"
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	summary, visited := filepath.Join(dir, "summary"), filepath.Join(dir, "visited")
	if err := os.WriteFile(summary, []byte("real check\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", "limit() { shift; \"$@\"; }\ntmp=$TEST_GATES_TMP\n"+invocation)
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "GITHUB_STEP_SUMMARY="+summary, "TEST_GATES_TMP="+dir, "TEST_VISITED="+visited)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Go test gate: %v\n%s", err, out)
	}
	if _, err := os.Stat(visited); err != nil {
		t.Fatal("test gate was not executed")
	}
	b, err = os.ReadFile(summary)
	if err != nil || string(b) != "real check\n" {
		t.Fatalf("fixture polluted real summary: %s (%v)", b, err)
	}
}
