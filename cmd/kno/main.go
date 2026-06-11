package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/buildjail"
	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/peers"
	"github.com/katzenpost/kno/internal/render"
	"github.com/katzenpost/kno/internal/sign"
	"github.com/katzenpost/kno/internal/topology"
	"github.com/katzenpost/kno/internal/track"
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
		track.Command(),
		render.Command(),

		topology.Command(),
	)

	honoured := []string{}
	readOnly := []string{
		"config", "peers",
		"release", "release signers", "release verify",
		"track",
	}

	unsupported := []string{
		"release sign",
		"render",
		"topology-propose",
	}
	if err := cli.Declare(root, honoured, readOnly, unsupported); err != nil {

		panic(err)
	}
	return root
}

func main() {
	if code, ok := buildjail.Dispatch(os.Args); ok {
		os.Exit(code)
	}
	if c, err := config.Load(config.ResolvePath("")); err == nil {
		if _, h, ok := config.LocalHost(c); ok {
			if err := buildjail.Enter(h.BuildJail); err != nil {
				fmt.Fprintln(os.Stderr, "kno: the build jail:", err)
				os.Exit(1)
			}
		}
	}
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
