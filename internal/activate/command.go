package activate

import (
	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/cli"
)

func Command() *cobra.Command {
	return &cobra.Command{
		Use:                "activate [--restart=<unit>,...] <release-dir> <tag> | --kno <deb>...",
		Short:              "install a built release on this host and start its services",
		DisableFlagParsing: true,
		SilenceUsage:       true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cli.Exit(Run(cmd.ErrOrStderr(), args))
		},
	}
}
