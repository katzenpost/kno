package track

import (
	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/cli"
)

func Command() *cobra.Command {
	return &cobra.Command{
		Use:                "track",
		Short:              "print the target tag",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		SilenceUsage:       true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cli.Exit(Run(cmd.OutOrStdout(), cmd.ErrOrStderr(), args))
		},
	}
}
