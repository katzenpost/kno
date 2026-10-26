package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/katzenpost/kno/internal/adopt"
	"github.com/katzenpost/kno/internal/attest"

	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/activate"
	"github.com/katzenpost/kno/internal/backupkeys"
	"github.com/katzenpost/kno/internal/build"
	"github.com/katzenpost/kno/internal/check"
	"github.com/katzenpost/kno/internal/clean"
	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/contribute"
	"github.com/katzenpost/kno/internal/doctor"
	"github.com/katzenpost/kno/internal/hostsetup"
	"github.com/katzenpost/kno/internal/nodeedit"
	"github.com/katzenpost/kno/internal/peers"
	"github.com/katzenpost/kno/internal/pkg/knodeb"
	"github.com/katzenpost/kno/internal/preflight"
	"github.com/katzenpost/kno/internal/probe"
	"github.com/katzenpost/kno/internal/push"
	"github.com/katzenpost/kno/internal/quarantine"
	"github.com/katzenpost/kno/internal/render"
	"github.com/katzenpost/kno/internal/schedule"
	"github.com/katzenpost/kno/internal/setup"
	"github.com/katzenpost/kno/internal/sign"
	"github.com/katzenpost/kno/internal/sim"
	"github.com/katzenpost/kno/internal/status"
	"github.com/katzenpost/kno/internal/topology"
	"github.com/katzenpost/kno/internal/track"
	"github.com/katzenpost/kno/internal/update"
	"github.com/katzenpost/kno/internal/verify"
)

func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:          "kno",
		Short:        "build, install and check a katzenpost network",
		SilenceUsage: true,

		SilenceErrors: true,

		Version: cli.Version(),
	}
	root.SetVersionTemplate("kno {{.Version}}\n")

	var noMultiplex bool
	root.PersistentFlags().BoolVar(&noMultiplex, "no-ssh-multiplex", false,
		"do not share one authenticated ssh connection per host (KNO_SSH_MULTIPLEX=0)")

	cli.Bind(root)
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if noMultiplex {
			if err := os.Setenv("KNO_SSH_MULTIPLEX", "0"); err != nil {
				return err
			}
		}

		if os.Getenv("V") == "1" || os.Getenv("KNO_VERBOSE") == "1" {
			cli.SetVerbose(true)
		}
		if os.Getenv("DRY_RUN") == "1" {
			cli.SetDryRun(true)
		}
		return cli.CheckDryRun(cmd)
	}

	root.SetFlagErrorFunc(cli.FlagError)
	root.AddCommand(
		sign.Command(),
		config.Command(),
		peers.Command(),
		build.Command(),
		sim.Command(),
		preflight.Command(),
		status.Command(),
		track.Command(),
		render.Command(),
		fpkgCommand(),

		update.Command(),
		quarantine.Command(),
		activate.Command(),
		topology.Command(),
		contribute.Command(),
		nodeedit.Command(),
		push.Command(),
		schedule.Command(),
		verify.Command(),
		probe.Command(),
		check.Command(),
		setup.Command(),
		doctor.Command(),
		clean.Command(),
		backupkeys.Command(),
		hostsetup.DepsCommand(),
		hostsetup.BootstrapCommand(),
		knodeb.Command(),
		adopt.Command(),
		attest.Command(),
	)

	honoured := []string{
		"activate", "adopt", "attest", "backup-keys", "bootstrap", "build", "build warped",
		"clean", "contribute", "deps", "doctor", "edit", "push",
		"schedule",
		"update",
	}
	readOnly := []string{
		"build compare", "build epoch", "build version", "config", "doctor package", "doctor tools", "peers",
		"preflight", "probe", "release", "release signers", "release verify",
		"sim", "sim validate", "status", "track", "verify",
	}

	unsupported := []string{
		"build pin",
		"build record",
		"build sysroot",
		"build vmimage",
		"check",
		"fpkg",
		"knodeb",
		"release sign",
		"render",
		"setup",
		"sim all", "sim configs", "sim down", "sim keys", "sim message",
		"sim probe", "sim up", "sim upgrade", "sim verify",
		"topology-propose",
		"unquarantine",
	}
	if err := cli.Declare(root, honoured, readOnly, unsupported); err != nil {

		panic(err)
	}
	return root
}

func main() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		compile.RemoveContainers()
		os.Exit(130)
	}()
	err := newRootCommand().Execute()
	if err == nil {
		return
	}

	if msg := err.Error(); msg != "" {
		fmt.Fprintln(os.Stderr, "Error:", msg)
	}

	var ec interface{ ExitCode() int }
	if errors.As(err, &ec) {
		os.Exit(ec.ExitCode())
	}
	os.Exit(1)
}
