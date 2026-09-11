package backupkeys

import (
	"fmt"
	"github.com/katzenpost/kno/internal/cli"
	"os"

	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/track"
)

func Command() *cobra.Command {
	var dest string
	cmd := &cobra.Command{
		Use:          "backup-keys [service...]",
		Short:        "copy a node's secret keys out to a backup",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := track.LoadEnv()
			if err != nil {
				return err
			}
			o := Options{
				Repo: e.Repo, Network: e.Network,
				Services: args, Dest: dest, DryRun: cli.DryRun(),
			}
			if len(o.Services) == 0 {
				o.Services = e.Services
			}
			if o.Dest == "" {
				o.Dest = DefaultDest(e)
			}
			res, err := Run(o)
			if err != nil {
				return err
			}
			n := 0
			for _, r := range res {
				n += len(r.Files)
			}
			fmt.Fprintf(os.Stderr, "kno backup-keys: %d key(s) from %d service(s) under %s\n",
				n, len(res), o.Dest)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&dest, "dest", "", "backup directory (default: $KEYBACKUP_DIR, or ~/keybackup)")
	return cmd
}
