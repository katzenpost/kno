package schedule

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/katzenpost/kno/internal/sshx"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/service"
	"github.com/katzenpost/kno/internal/track"
)

func ModeFor(h config.Host) string {
	if h.Scheduler == "" {
		return ModeNone
	}
	return h.Scheduler
}

func KnownMode(s string) bool {
	return s == ModeTimer || s == ModeCron || s == ModeNone
}

func RunStep(ctx context.Context, s Step, w io.Writer, dry bool) error {
	if dry {
		fmt.Fprintf(w, "DRYRUN: %s\n", strings.Join(s.Argv, " "))
		if s.Stdin != "" {
			fmt.Fprintf(w, "DRYRUN: on stdin:\n%s", s.Stdin)
		}
		return nil
	}
	cmd := exec.CommandContext(ctx, s.Argv[0], s.Argv[1:]...)
	cmd.Stdout, cmd.Stderr = w, w
	cmd.Stdin = os.Stdin
	if s.Stdin != "" {
		cmd.Stdin = strings.NewReader(s.Stdin)

		cmd.Stdout = io.Discard
	}
	if err := cmd.Run(); err != nil && !s.BestEffort {
		return fmt.Errorf("%s: %w", strings.Join(s.Argv, " "), err)
	}
	return nil
}

func Apply(ctx context.Context, p Plan, w io.Writer, dry bool) error {
	for _, s := range p.Steps {
		if err := RunStep(ctx, s, w, dry); err != nil {
			return err
		}
	}
	return nil
}

func Run(stderr io.Writer, args []string, host string, dry bool) int {
	l := track.Logger{Tool: "schedule", W: stderr}
	e, err := track.LoadEnv()
	if err != nil {
		fmt.Fprintf(stderr, "kno: config error (%v). Fix your kno.toml or unset it\n", err)
		return 1
	}

	o, err := options(e, args, host, dry)
	if err != nil {
		l.Errorf("%v", err)
		return 1
	}
	p, err := PlanFor(o)
	if err == nil && p.Local() {
		err = localCanSchedule(runtime.GOOS, p.Mode)
	}
	if err != nil {
		l.Errorf("%v", err)
		return 1
	}
	if err := Apply(context.Background(), p, stderr, o.DryRun); err != nil {
		l.Errorf("%v", err)
		return 1
	}
	verb := "set"
	if o.DryRun {
		verb = "would be set"
	}
	if p.Local() {
		l.Logf("scheduler '%s' %s on this host", p.Mode, verb)
	} else {
		l.Logf("scheduler '%s' %s on %s", p.Mode, verb, p.Host)
	}
	return 0
}

var localGOOS = runtime.GOOS

func options(e track.Env, args []string, hostFlag string, dry bool) (Options, error) {

	mode := ""
	if len(args) > 0 {
		if !KnownMode(args[0]) {
			return Options{}, unknownMode(args[0])
		}
		mode, args = args[0], args[1:]
	}
	hostArg := hostFlag
	if len(args) > 0 {
		hostArg, args = args[0], args[1:]
	}
	if len(args) > 0 {
		return Options{}, fmt.Errorf("too many arguments: %s", strings.Join(args, " "))
	}

	o := Options{
		Repo:   e.Repo,
		Mode:   mode,
		Host:   hostArg,
		SSH:    sshx.Program(),
		User:   envOr("KNO_USER", DefaultUser),
		DryRun: dry,
	}

	o.Sudo = cli.Sudo()

	if h, ok := e.Config.Hosts[hostArg]; ok && hostArg != "" {
		o.Host = hostArg
		if h.SSH != "" {
			o.Host = h.SSH
		}
		if local, _, ok := config.LocalHost(e.Config); ok && local == hostArg {
			o.Host = ""
		}
		o.FreeBSD = service.AllEzjail(e.Repo, e.Network, config.HostServices(e.Repo, e.Config, h))
		if o.Mode == "" {
			o.Mode = ModeFor(h)
		}
	} else if hostArg == "" && o.Mode == "" {
		_, h, ok := config.LocalHost(e.Config)
		if !ok {
			return Options{}, fmt.Errorf("this host is not in kno.toml, so kno.toml" +
				" sets no scheduler here. Name a mode (timer, cron or none) to change this" +
				" machine, or name a host")
		}
		o.Mode = ModeFor(h)
	}
	if o.Mode == "" {
		o.Mode = ModeNone
	}
	if o.Host == "" && localGOOS == "freebsd" {
		o.FreeBSD = true
	}
	label := hostArg
	if label == "" {
		label, _, _ = config.LocalHost(e.Config)
	}
	if label != "" {
		o.Privilege = e.Config.Privilege(label)
		o.Sudo = cli.RootHelper(o.Privilege)
	}
	return o, nil
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
