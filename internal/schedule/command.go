package schedule

import (
	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/cli"
)

func Command() *cobra.Command {
	var host string

	cmd := &cobra.Command{
		Use:          "schedule [timer|cron|none] [host]",
		Short:        "install or remove the update timer for a host",
		Args:         cobra.MaximumNArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cli.Exit(Run(cmd.ErrOrStderr(), args, host, cli.DryRun()))
		},
	}

	f := cmd.Flags()
	f.StringVar(&host, "host", "", "host label in kno.toml, or an ssh target")
	return cmd
}
