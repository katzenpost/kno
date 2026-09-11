package push

import (
	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/cli"
)

func Command() *cobra.Command {
	var mode string

	cmd := &cobra.Command{
		Use:          "push <host-label> <tag>",
		Short:        "install the packages of a tag on a host over ssh",
		Args:         cli.Args(cobra.ExactArgs(2)),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cli.Exit(Run(cmd.ErrOrStderr(), args[0], args[1], mode, cli.DryRun()))
		},
	}

	f := cmd.Flags()
	f.StringVar(&mode, "mode", "", "push mode: copy, install or full (default: [push] mode in kno.toml)")
	return cmd
}
