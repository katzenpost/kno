package status

import (
	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/cli"
)

func Command() *cobra.Command {
	return &cobra.Command{
		Use:                "status",
		Short:              "show the config, the source, the target tag and the state of each host and service",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		SilenceUsage:       true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cli.Exit(Run(cmd.OutOrStdout(), cmd.ErrOrStderr(), args))
		},
	}
}
