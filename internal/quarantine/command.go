package quarantine

import (
	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/cli"
)

func Command() *cobra.Command {
	var host string
	var all bool

	cmd := &cobra.Command{
		Use:          "unquarantine [tag]",
		Short:        "let a host try a quarantined release again",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			tag := ""
			if len(args) == 1 {
				tag = args[0]
			}
			return cli.Exit(Run(cmd.ErrOrStderr(), tag, host, all))
		},
	}
	f := cmd.Flags()
	f.StringVar(&host, "host", "", "clear this organized host's quarantine")
	f.BoolVar(&all, "all", false, "clear it for every host this machine organizes")
	return cmd
}
