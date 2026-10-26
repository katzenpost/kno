package doctor

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/argcheck"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/hostsel"
	"github.com/katzenpost/kno/internal/sshx"
	"github.com/katzenpost/kno/internal/track"
)

func RunRemoteHosts(w io.Writer, sel hostsel.Flags, fix, dry bool) int {
	e, err := track.LoadEnv()
	if err != nil {
		fmt.Fprintf(w, "kno cannot read the config: %v. Correct kno.toml, or unset KNO_CONFIG.\n", err)
		return 1
	}
	labels, err := hostsel.Labels(e, sel)
	if err != nil {
		fmt.Fprintf(w, "kno: %v\n", err)
		return 1
	}
	rc := 0
	for i, label := range labels {
		if i > 0 {
			fmt.Fprintln(w)
		}
		if code := runRemote(w, e, label, fix, dry, sel.Verbose()); code != 0 {
			rc = 1
		}
	}
	return rc
}

func runRemote(w io.Writer, e track.Env, label string, fix, dry, verbose bool) int {
	node, err := nodeFor(e, label)
	if err != nil {
		fmt.Fprintf(w, "kno: %v\n", err)
		return 1
	}
	if err := argcheck.Target("ssh target", node); err != nil {
		fmt.Fprintf(w, "kno: %v\n", err)
		return 1
	}

	remote := remoteDoctorCmdFor(label, fix, dry, verbose)

	fmt.Fprintf(w, "== %s (%s) ==\n", label, node)
	argv := sshx.SSH(sshx.Program(), node, remote)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = w, w

	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			if ee.ExitCode() == sshUnreachable {
				fmt.Fprintf(w, "kno: could not connect to %s. kno did not check it\n", node)
			}
			return ee.ExitCode()
		}
		fmt.Fprintf(w, "kno: could not run doctor on %s: %v\n", node, err)
		return 1
	}
	return 0
}

const sshUnreachable = 255

func nodeFor(e track.Env, label string) (string, error) {
	if _, ok := e.Config.Hosts[label]; !ok {
		var known []string
		for l := range e.Config.Hosts {
			known = append(known, l)
		}
		sort.Strings(known)
		if len(known) == 0 {
			return "", fmt.Errorf("no hosts in %s. Run 'kno setup'", orNone(e.ConfigPath))
		}
		return "", fmt.Errorf("no such host %q (this organizer has %s)",
			label, strings.Join(known, " "))
	}
	node := config.SSHTarget(e.Config, label)
	if node == "" {
		return "", fmt.Errorf("%s is this host. Run 'kno doctor' without --host", label)
	}
	return node, nil
}

func orNone(s string) string {
	if s == "" {
		return "(no config)"
	}
	return s
}

func remoteDoctorCmdFor(label string, fix, dry, verbose bool) string {
	cmd := "kno doctor"
	if label != "" {
		cmd = "KNO_DOCTOR_LABEL=" + label + " kno doctor"
	}
	if verbose {
		cmd += " --verbose"
	}
	if fix {
		cmd += " --fix"

		if dry {
			cmd += " --dry-run"
		}
	}
	return sshx.AsRoot(cmd)
}
