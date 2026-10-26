package verify

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/sshx"
	"github.com/katzenpost/kno/internal/track"
)

func fakeSSH(t *testing.T, body string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ssh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSH", p)
	t.Setenv("KNO_SSH_MULTIPLEX", "0")
}

func oneHost(ssh string) track.Env {
	return track.Env{Config: config.Config{Hosts: map[string]config.Host{
		"mixy": {SSH: ssh},
	}}}
}

func TestARemoteExitThatIsNoVerifyResultIsNotYet(t *testing.T) {
	for _, c := range []struct {
		name, script string
		want         int
	}{
		{"killed by a signal", "kill -KILL $$", CodeNotYet},
		{"an exit code verify does not give", "exit 7", CodeNotYet},
		{"failed", "exit 2", CodeFailed},
		{"cannot ask", "exit 4", CodeCannotAsk},
	} {
		t.Run(c.name, func(t *testing.T) {
			fakeSSH(t, c.script)
			var out bytes.Buffer
			l := track.Logger{Tool: "verify", W: &out}
			if got := runHost(&out, l, oneHost("root@mixy"), "mixy", false, "kno verify"); got != c.want {
				t.Fatalf("exit = %d, want %d:\n%s", got, c.want, out.String())
			}
		})
	}
}

func TestAFailedHostWinsOverTheOthers(t *testing.T) {
	for _, c := range []struct{ codes []int }{
		{[]int{CodeFailed, CodeNoConsensus}},
		{[]int{CodeCannotAsk, CodeFailed}},
		{[]int{CodeNotYet, CodeFailed, CodeCannotAsk}},
	} {
		worst := 0
		for _, code := range c.codes {
			worst = worse(worst, code)
		}
		if worst != CodeFailed {
			t.Errorf("%v gives %d, want %d: a failed host must roll back", c.codes, worst, CodeFailed)
		}
	}
	if got := worse(worse(0, CodeNotYet), CodeNoConsensus); got != CodeNoConsensus {
		t.Errorf("with no failed host the highest code wins, got %d", got)
	}
}

func TestAFlagErrorIsNotAFailedNode(t *testing.T) {
	root := &cobra.Command{Use: "kno", SilenceUsage: true, SilenceErrors: true}
	root.SetFlagErrorFunc(cli.FlagError)
	root.AddCommand(Command())
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"verify", "--no-such-flag"})
	err := root.Execute()
	var ec interface{ ExitCode() int }
	if !errors.As(err, &ec) {
		t.Fatalf("err = %v, want an exit code", err)
	}
	if ec.ExitCode() != CodeNotYet {
		t.Errorf("exit = %d, want %d: exit 2 makes kno update roll back", ec.ExitCode(), CodeNotYet)
	}
}

func TestAHostWithNoSSHIsReachedAtItsLabel(t *testing.T) {
	fakeSSH(t, `echo "$@" > "$(dirname "$0")/argv"`)
	t.Setenv("KNO_HOST", "")
	e := track.Env{Config: config.Config{Hosts: map[string]config.Host{
		"mixy": {}, "reed": {},
	}}}
	var out strings.Builder
	l := track.Logger{Tool: "verify", W: &out}
	if code := runHost(&out, l, e, "mixy", false, "kno verify"); code != CodeHealthy {
		t.Fatalf("exit = %d:\n%s", code, out.String())
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(os.Getenv("SSH")), "argv"))
	if err != nil || !strings.HasPrefix(string(b), strings.Join(append(sshx.Bounds, "mixy "), " ")) {
		t.Errorf("ssh got %q (%v), want the label as the target", b, err)
	}

	t.Setenv("KNO_HOST", "mixy")
	out.Reset()
	if code := runHost(&out, l, e, "mixy", false, "kno verify"); code != CodeNotYet ||
		!strings.Contains(out.String(), "is this host") {
		t.Errorf("exit = %d: this host must not be reached over ssh:\n%s", code, out.String())
	}
}
