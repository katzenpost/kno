package adopt

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/track"
)

func Command() *cobra.Command {
	var o Options
	cmd := &cobra.Command{
		Use:          "adopt",
		Short:        "add the newest katzenpost tag to kno, build it, record it and merge it",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := track.LoadEnv()
			if err != nil {
				return fmt.Errorf("kno cannot read the config: %v", err)
			}
			if _, err := os.Stat(e.Repo); err != nil || e.Repo == "" {
				return fmt.Errorf("adopt needs a kno checkout; run it inside one")
			}
			o.Env, o.Out, o.Verbose = e, cmd.ErrOrStderr(), cli.Verbose()
			if cli.DryRun() {
				s := DefaultSteps()
				if err := s.Fetch(e); err != nil {
					return err
				}
				next, known, err := Newest(e)
				if err != nil {
					return err
				}
				if next == "" {
					fmt.Fprintf(cmd.ErrOrStderr(), "--dry-run: no katzenpost tag is newer than %s\n", known)
					return nil
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "--dry-run: would adopt %s on the branch katzenpost-%s\n", next, next)
				return nil
			}
			tag, err := Run(o, DefaultSteps())
			if err != nil {
				return cli.Failf(cmd.ErrOrStderr(), 1, "kno adopt: %v", err)
			}
			if tag != "" {
				fmt.Fprintln(cmd.OutOrStdout(), tag)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.Tag, "tag", "", "adopt this tag instead of the newest one")
	f.BoolVar(&o.Sim, "sim", false, "rehearse the network in the simulator before the merge")
	f.BoolVar(&o.Push, "push", false, "push the branch after the merge")
	return cmd
}
