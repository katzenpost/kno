package topology

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/config"
)

func Command() *cobra.Command {
	var network, out string
	var ask, apply bool
	cmd := &cobra.Command{
		Use:          "topology-propose",
		Short:        "propose a layer for every mix that is in no topology layer",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo := config.RepoDir()
			if repo == "" {
				return fmt.Errorf("cannot locate the network repo (set KNO_REPO)")
			}
			p, err := Propose(Options{Repo: repo, Network: network, Ask: ask})
			if err != nil {
				return err
			}
			if len(p.Placements) == 0 {
				fmt.Fprintln(os.Stderr, "kno topology-propose: every mix is in a layer. Nothing to propose")
				return nil
			}
			fmt.Print(p.Diff)
			switch {
			case apply:
				path := ManifestPath(repo, network)
				if err := Write(p, path); err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "kno topology-propose: wrote %s\n", path)
				fmt.Fprintln(os.Stderr, "  next: kno render, then review the generated configs and commit")
			case out != "":
				if err := Write(p, out); err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "kno topology-propose: wrote %s\n", out)
			default:
				fmt.Fprintln(os.Stderr, "kno topology-propose: nothing written"+
					" (--out FILE to save it, --apply to edit the manifest here)")
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&network, "network", "namenlos", "network name")
	f.StringVar(&out, "out", "", "write the proposed manifest to this file")
	f.BoolVar(&ask, "ask", false, "prompt for each mix's layer instead of taking the thinnest")
	f.BoolVar(&apply, "apply", false, "write the proposal back into the repo's manifest")
	return cmd
}
