package push

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/katzenpost/kno/internal/sshx"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/knover"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/pkg/knodeb"
	"github.com/katzenpost/kno/internal/source"
	"github.com/katzenpost/kno/internal/track"
)

func RunStep(ctx context.Context, s Step, stdout, stderr io.Writer, dry bool) error {
	if dry {
		fmt.Fprintf(stderr, "DRYRUN: %s\n", strings.Join(s.Argv, " "))
		return nil
	}
	cmd := exec.CommandContext(ctx, s.Argv[0], s.Argv[1:]...)
	if len(s.Env) > 0 {
		cmd.Env = append(os.Environ(), s.Env...)
	}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", strings.Join(s.Argv, " "), err)
	}
	return nil
}

func ApplyLabelled(ctx context.Context, p Plan, l track.Logger, dry bool, label string) (int, error) {

	out := io.Writer(l.W)
	if f, isFile := l.W.(*os.File); !isFile || !cli.IsTerminal(f) {
		out = cli.NewPrefixWriter(l.W, cli.HostPrefix(label))
		if c, ok := out.(io.Closer); ok {
			defer func() { _ = c.Close() }()
		}
	}
	warnings := 0
	for _, s := range p.Steps {
		if err := RunStep(ctx, s, out, out, dry); err != nil {
			if !s.BestEffort {
				return warnings, err
			}
			warnings++
			l.Warnf("%v (continuing)", err)
			continue
		}
		if s.Note != "" {
			l.Logf("%s", s.Note)
		}
	}
	return warnings, nil
}

func Run(stderr io.Writer, label, tag, mode string, dry bool) int {
	l := track.Logger{Tool: "push", W: stderr}
	e, err := track.LoadEnv()
	if err != nil {
		fmt.Fprintf(stderr, "kno: config error (%v). Fix your kno.toml or unset it\n", err)
		return 1
	}

	o, err := options(e, label, tag, mode, dry)
	if err != nil {
		l.Errorf("%v", err)
		return 1
	}
	if f, ok := stderr.(*os.File); ok {
		o.Progress = cli.IsTerminal(f)
	}

	if err := checkRemote(o); err != nil {
		l.Errorf("%v", err)
		return 1
	}
	if err := requireSystemd(o); err != nil {
		l.Errorf("%v", err)
		return 1
	}
	ctx := context.Background()
	o = Probe(ctx, o, l.Warnf)

	if NeedsBuild(o.Rel) {
		l.Logf("%s is not built. kno build runs first.", o.Tag)

		if err := RunStep(ctx, BuildStep(o), io.Discard, stderr, o.DryRun); err != nil {
			l.Errorf("%v", err)
			return 1
		}
	}

	p, err := PlanFor(o)
	if err != nil {
		l.Errorf("%v", err)
		return 1
	}
	switch o.Mode {
	case ModeCopy:
		l.Logf("push copy to %s: copy the packages of %s to %s only. No root. No install.",
			o.Node, o.Label, RemoteRelease(o.Tag))
	default:
		l.Logf("push %s to %s: copy the packages of %s, then run kno activate on %s.",
			o.Mode, o.Node, o.Label, o.Label)
	}
	warnings, err := ApplyLabelled(ctx, p, l, o.DryRun, o.Label)
	if err != nil {
		l.Errorf("%v", err)
		return 1
	}
	if o.Mode == ModeCopy {
		l.Logf("To install them, run on %s: sudo kno activate %s %s", o.Label, RemoteRelease(o.Tag), o.Tag)
	}
	if err := recordDelivery(o, warnings); err != nil {
		l.Warnf("kno could not write the push receipt: %v. kno status then does not show this push.", err)
	}
	if warnings > 0 {
		l.Logf("push %s -> %s (%s) complete with %d warning(s). See the WARN lines above",
			o.Mode, o.Node, o.Tag, warnings)
		return 0
	}
	l.Logf("push %s -> %s (%s) complete", o.Mode, o.Node, o.Tag)
	return 0
}

func Preflight(e track.Env, label, tag, mode string) error {
	o, err := options(e, label, tag, mode, false)
	if err != nil {
		return err
	}
	_, err = PlanFor(o)
	return err
}

func recordDelivery(o Options, warnings int) error {
	if o.DryRun {
		return nil
	}
	return knopath.RecordPush(o.Tag, o.Label,
		fmt.Sprintf("%s mode=%s node=%s warnings=%d", o.Tag, o.Mode, o.Node, warnings))
}

func options(e track.Env, label, tag, mode string, dry bool) (Options, error) {
	o := Options{
		Repo:    e.Repo,
		Network: e.Network,
		Label:   label,
		Node:    label,
		Tag:     tag,

		Rel:   releaseDir(tag),
		Mode:  mode,
		SSH:   sshx.Program(),
		SCP:   cmp.Or(os.Getenv("SCP"), "scp"),
		RSYNC: cmp.Or(os.Getenv("RSYNC"), "rsync"),

		RemoteRsync: true,

		KnoVersion: KnoVersionFor(e.Repo, tag),
		DryRun:     dry || os.Getenv("DRY_RUN") == "1",
	}
	if o.Mode == "" {
		o.Mode = e.PushMode
	}

	if e.ConfigPath == "" {

		o.Services = e.Services
		return o, nil
	}
	h, ok := e.Config.Hosts[label]
	if !ok {
		return Options{}, fmt.Errorf("host %q not in %s", label, e.ConfigPath)
	}
	if h.SSH != "" {
		o.Node = h.SSH
	}
	o.Services = config.HostServices(e.Repo, e.Config, h)
	if len(o.Services) > 0 {
		node := NodeOf(o.Services[0])
		o.Arch = config.NodeArch(e.Repo, o.Network, node)
		o.OS = config.NodeOS(e.Repo, o.Network, node)
	}
	return o, nil
}

func KnoVersionFor(repo, tag string) string {
	if repo == "" {
		return ""
	}
	pins, err := source.ParsePins(filepath.Join(repo, "thirdparty", "pins.env"))
	if err != nil || pins["KATZENPOST_REF"] == "" {
		return ""
	}
	if want, _ := deb.SplitRevision(tag); want != pins["KATZENPOST_REF"] {
		return ""
	}
	return knodeb.ToolVersion(repo, tag, knover.Revision(repo, tag))
}
