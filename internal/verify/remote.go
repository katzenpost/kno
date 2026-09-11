package verify

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/katzenpost/kno/internal/argcheck"
	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/hostsel"
	"github.com/katzenpost/kno/internal/sshx"
	"github.com/katzenpost/kno/internal/track"
)

const sshUnreachable = 255

func RunHosts(w io.Writer, sel hostsel.Flags, f Flags, args []string) int {
	l := track.Logger{Tool: "verify", W: w}
	e, err := track.LoadEnv()
	if err != nil {
		l.Errorf("kno cannot read the config: %v. Correct kno.toml, or unset KNO_CONFIG.", err)
		return CodeNotYet
	}
	labels, err := hostsel.Labels(e, sel)
	if err != nil {
		l.Errorf("%v", err)
		return CodeNotYet
	}
	worst, failed := 0, 0
	for i, label := range labels {
		if i > 0 {
			fmt.Fprintln(w)
		}
		code := runHost(w, l, e, label, sel.Verbose(), f.remoteCommand(args))
		if code != 0 {
			failed++
			worst = worse(worst, code)
		}
	}
	if len(labels) > 1 {
		l.Logf("%d of %d host(s) verified", len(labels)-failed, len(labels))
	}
	return worst
}

func runHost(w io.Writer, l track.Logger, e track.Env, label string, verbose bool, remote string) int {
	if _, ok := e.Config.Hosts[label]; !ok {
		l.Errorf("no such host %q in %s", label, e.ConfigPath)
		return CodeNotYet
	}
	node := config.SSHTarget(e.Config, label)
	if node == "" {
		l.Errorf("%s is this machine; run 'kno verify' without --host", label)
		return CodeNotYet
	}
	if err := argcheck.Target("ssh target", node); err != nil {
		l.Errorf("%v", err)
		return CodeNotYet
	}

	fmt.Fprintf(w, "== %s (%s) ==\n", label, node)

	argv := sshx.SSH(sshx.Program(), node, remote)
	cmd := exec.Command(argv[0], argv[1:]...)
	out := cli.NewPrefixWriter(w, cli.HostPrefix(label))
	cmd.Stdout, cmd.Stderr = out, out
	cmd.Stdin = os.Stdin
	err := cmd.Run()
	if c, ok := out.(io.Closer); ok {
		_ = c.Close()
	}
	if err == nil {
		if verbose {
			l.Logf("%s: verified (exit 0)", label)
		}
		return 0
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		l.Errorf("%s: could not run verify on %s: %v", label, node, err)
		return CodeNotYet
	}
	switch code := ee.ExitCode(); code {
	case sshUnreachable:

		l.Errorf("%s: ssh cannot connect to %s. kno has no result for this host.",
			label, node)
		return CodeNotYet
	case 127:
		l.Errorf("%s: ssh found no kno command on %s (exit 127). Install kno on %s.", label, node, label)
		return CodeNotYet
	default:
		if code < CodeHealthy || code > CodeCannotAsk {
			l.Errorf("%s: kno verify on %s stopped with exit %d, which is not a"+
				" verify result. kno has no result for this host.", label, node, code)
			return CodeNotYet
		}
		l.Errorf("%s: %s", label, Verdict(code))
		return code
	}
}

func worse(a, b int) int {
	if a == CodeFailed || b == CodeFailed {
		return CodeFailed
	}
	return max(a, b)
}

func Enabled(o Options) string {
	var on []string
	for _, c := range []struct {
		yes  bool
		name string
	}{
		{o.checksUnits(), "units"},
		{true, "consensus"},
		{o.checksParticipation(), "participation"},
		{o.checksDataplane() && o.Echo, "echo"},
		{o.checksDataplane() && o.Pigeonhole, "pigeonhole"},
	} {
		if c.yes {
			on = append(on, c.name)
		}
	}
	return strings.Join(on, " ")
}
