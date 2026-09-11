package activate

import (
	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/cli"
)

func Command() *cobra.Command {
	return &cobra.Command{
		Use:          "activate <release-dir> <tag>",
		Short:        "install a built release on this host and start its services",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cli.Exit(Run(cmd.ErrOrStderr(), args))
		},
	}
}
