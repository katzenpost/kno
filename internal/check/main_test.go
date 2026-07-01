package check

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

func TestMainReportsAndExits(t *testing.T) {
	if what := os.Getenv("KNO_CHECK_MAIN"); what != "" {
		args := append([]string{what, "--repo", os.Getenv("KNO_CHECK_REPO")},
			strings.Fields(os.Getenv("KNO_CHECK_ARGS"))...)
		c := Command()
		c.SetArgs(args)
		err := c.Execute()
		if err == nil {
			return
		}

		var ec interface{ ExitCode() int }
		if errors.As(err, &ec) {
			os.Exit(ec.ExitCode())
		}
		os.Exit(1)
	}
	repo := testutil.RepoRoot(t)

	unreadable := t.TempDir()
	if err := os.MkdirAll(filepath.Join(unreadable, "networks", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	newGap := t.TempDir()
	lintNetwork(t, newGap, "y", []string{"aaa"}, []string{"zzz"})

	for _, tc := range []struct {
		name, what, args, repo, want string
		exit                         int
	}{
		{name: "render", what: "render", repo: repo, want: "configs match"},
		{name: "operator", what: "operator", repo: repo, want: "operator: "},
		{name: "topology", what: "topology", repo: repo, want: "known gap"},
		{name: "all", what: "all", repo: repo, want: "operator+render+topology: "},

		{
			name: "a new inconsistency fails", what: "topology", repo: newGap,
			args: "--network y", want: "carries no traffic", exit: 1,
		},

		{
			name: "unreadable network data fails", what: "topology", repo: unreadable,
			args: "--network x", want: "not linted", exit: 1,
		},

		{
			name: "nothing to lint fails", what: "topology", repo: unreadable,
			want: "nothing was linted", exit: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=TestMainReportsAndExits")

			cmd.Env = append(os.Environ(), "KNO_NETWORK=",
				"KNO_CHECK_MAIN="+tc.what, "KNO_CHECK_REPO="+tc.repo, "KNO_CHECK_ARGS="+tc.args)
			out, err := cmd.CombinedOutput()
			got := 0
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				got = ee.ExitCode()
			} else if err != nil {
				t.Fatalf("kno check %s: %v\n%s", tc.what, err, out)
			}
			if got != tc.exit {
				t.Errorf("kno check %s %s exited %d, want %d:\n%s", tc.what, tc.args, got, tc.exit, out)
			}
			if !strings.Contains(string(out), tc.want) {
				t.Errorf("kno check %s %s did not report %q:\n%s", tc.what, tc.args, tc.want, out)
			}
		})
	}
}

func TestEveryListedCheckRuns(t *testing.T) {
	repo := t.TempDir()
	for _, c := range checks {
		args := []string{c[0], "--repo", repo, "--network", "n"}
		if c[0] == "repro" {
			args = append(args, "--plan")
		}
		cmd := Command()
		var out, errOut strings.Builder
		cmd.SetOut(&out)
		cmd.SetErr(&errOut)
		cmd.SetArgs(args)
		_ = cmd.Execute()
		if strings.HasPrefix(errOut.String(), "usage:") {
			t.Errorf("kno check %s is listed but runs nothing:\n%s", c[0], errOut.String())
		}
	}
}
