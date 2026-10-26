package sim

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"text/tabwriter"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/source"
	"github.com/katzenpost/kno/internal/track"
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "sim",
		Short:        "run a test network with the binaries of a release",
		SilenceUsage: true,
	}
	cmd.AddCommand(validateCommand())
	for _, st := range Stages {
		cmd.AddCommand(stageCommand(st))
	}
	return cmd
}

var stageHelp = map[string]string{
	"keys":    "build the packages and tools, and make a fresh keyset",
	"configs": "render the configs from the network data with the fresh keys",
	"up":      "start the containers, install katzenpost, wait for consensus",
	"verify":  "run 'kno verify' inside the client container",
	"message": "send an echo ping and a pigeonhole round trip",
	"probe":   "run 'kno probe' against every server and the consensus",
	"upgrade": "rehearse the upgrade of the operator's host platforms",
	"down":    "remove the containers and the network of this run",
	"all":     "run every stage in order, then remove the network if all pass",
}

func stageCommand(stage string) *cobra.Command {
	var o NetOptions
	cmd := &cobra.Command{
		Use:          stage,
		Short:        stageHelp[stage],
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := linuxOnly(runtime.GOOS); err != nil {
				return err
			}
			o.Out, o.Verbose = cmd.ErrOrStderr(), cli.Verbose()
			if err := o.resolve(); err != nil {
				return err
			}
			if err := RunNet(o, stage); err != nil {
				track.Logger{Tool: "sim", W: o.Out}.Errorf("%v", err)
				return cli.Exit(1)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.Work, "workdir", "", "work directory (default /var/tmp/kno-sim-<uid>-<net>)")
	f.StringVar(&o.Net, "net", "knosim", "podman network, label and container prefix")
	f.StringVar(&o.Network, "network", "namenlos", "the network to boot")
	f.DurationVar(&o.Epoch, "epoch", DefaultEpoch, "katzenpost epoch for every daemon; 2m or more")
	f.StringVar(&o.Pkgs, "pkgs", "", "use the katzenpost packages in this directory, not a new build")
	f.StringVar(&o.From, "from", "", "the previous release's packages, for the upgrade rehearsal")
	f.StringVar(&o.Rehearse, "rehearse", "reed,mixy,annares", "the nodes the upgrade rehearsal upgrades, in order")
	f.StringVar(&o.Pull, "pull", "annares", "the rehearsal nodes that upgrade with kno update on the node")
	f.StringVar(&o.Fail, "fail", "reed", "the rehearsal node whose first upgrade fails verify and rolls back")
	f.BoolVar(&o.Emulate, "emulate", false, "run each node on its own architecture through binfmt")
	f.BoolVar(&o.Keep, "keep", false, "keep the network after a run that passes")
	f.BoolVar(&o.NoCourier, "no-courier", false, "disable the couriers and skip cp-bench; give it to every stage")
	f.StringVar(&o.LogLevel, "log-level", "DEBUG", "daemon log level")
	return cmd
}

func (o *NetOptions) resolve() error {
	if err := CheckEpoch(o.Epoch); err != nil {
		return err
	}
	o.Repo = config.RepoDir()
	if o.Repo == "" {
		return fmt.Errorf("cannot locate the network data (set KNO_REPO)")
	}
	if strings.TrimSpace(o.Net) == "" {
		return fmt.Errorf("--net cannot be empty")
	}
	if o.Work == "" {
		o.Work = filepath.Join("/var/tmp", fmt.Sprintf("kno-sim-%d-%s", os.Getuid(), o.Net))
	}
	for _, p := range []*string{&o.Work, &o.Pkgs, &o.From} {
		if *p == "" {
			continue
		}
		abs, err := filepath.Abs(*p)
		if err != nil {
			return err
		}
		*p = abs
	}
	return nil
}

func validateCommand() *cobra.Command {
	var network, tag, binDir string
	cmd := &cobra.Command{
		Use:          "validate",
		Short:        "load every rendered config with its own daemon",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := linuxOnly(runtime.GOOS); err != nil {
				return err
			}
			repo := config.RepoDir()
			if repo == "" {
				return fmt.Errorf("cannot locate the network repo (set KNO_REPO)")
			}
			if tag == "" {
				pins, err := source.ParsePins(filepath.Join(repo, "thirdparty", "pins.env"))
				if err != nil {
					return fmt.Errorf("reading pins: %w", err)
				}
				if tag = pins["KATZENPOST_REF"]; tag == "" {
					return fmt.Errorf("no KATZENPOST_REF in pins.env; pass --tag")
				}
			}
			how := "given with --bin"
			if binDir == "" {
				d, h, cleanup, err := FindBinaries(repo, network, tag)
				if err != nil {
					return err
				}
				defer cleanup()
				binDir, how = d, h
			}
			o := Options{Repo: repo, Network: network, BinDir: binDir}
			done, fallback, err := o.Prepare()
			if err != nil {
				return err
			}
			defer done()
			if fallback != "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "sim validate: %s; each daemon runs on this host,"+
					" without a container and without the network's public keys\n", fallback)
			}
			r, err := Validate(o)
			if err != nil {
				return err
			}
			return cli.Exit(Report(cmd.OutOrStdout(), r, network, tag, how))
		},
	}
	f := cmd.Flags()
	f.StringVar(&network, "network", "namenlos", "network name")
	f.StringVar(&tag, "tag", "", "release to take binaries from (default: the pinned ref)")
	f.StringVar(&binDir, "bin", "", "directory holding the daemons, instead of finding them")
	return cmd
}

func Report(w io.Writer, r Result, network, tag, how string) int {
	fmt.Fprintf(w, "sim validate: %s at %s, binaries %s\n", network, tag, how)
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	for _, o := range r.Outcomes {
		mark := "  ok  "
		switch o.State {
		case "failed":
			mark = "  FAIL"
		case "needs the node", "no binary":
			mark = "  --  "
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", mark, o.Service, o.Binary, note(o))
	}
	tw.Flush()
	bad := r.Failed()
	fmt.Fprintf(w, "sim validate: %d accepted, %d refused, %d not checkable here\n",
		r.Checked(), bad, len(r.Outcomes)-r.Checked()-bad)
	if bad > 0 {
		fmt.Fprintf(w, "  A config this network renders is not one its own daemon will\n")
		fmt.Fprintf(w, "  load. That is a release not to deploy: the packages would\n")
		fmt.Fprintf(w, "  install and the service would fail to start.\n")
		return 1
	}
	if r.Checked() == 0 {
		fmt.Fprintf(w, "  No daemon accepted or refused a config, so this run checked nothing.\n")
		return 1
	}
	return 0
}

func note(o Outcome) string {
	if o.State == "ok" {
		return ""
	}
	if o.Detail == "" {
		return o.State
	}
	return o.State + ": " + o.Detail
}

func linuxOnly(goos string) error {
	if goos == "linux" {
		return nil
	}
	return fmt.Errorf("kno sim runs on Linux only: it unpacks the release with dpkg-deb,"+
		" runs the Linux daemons, and runs the network in systemd containers under podman;"+
		" this host runs %s", goos)
}
