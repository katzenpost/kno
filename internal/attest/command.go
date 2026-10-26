package attest

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/cli"
)

func Command() *cobra.Command {
	var key, repo string
	cmd := &cobra.Command{
		Use:          "attest [commit]",
		Short:        "sign a kno commit, or show its signatures",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rev := "HEAD"
			if len(args) == 1 {
				rev = args[0]
			}
			w := cmd.OutOrStdout()
			if key != "" && cli.DryRun() {
				fmt.Fprintf(w, "DRYRUN: sign %s with %s into %s\n", rev, key, NotesRef)
			} else if key != "" {
				sha, err := Sign(repo, rev, key)
				if err != nil {
					return err
				}
				fmt.Fprintf(w, "Signed %s. To publish the signature, run: git push origin %s\n", sha, NotesRef)
			}
			fmt.Fprintf(w, "%s: %s\n", rev, Check(repo, rev, SignersPath))
			return nil
		},
	}
	cmd.Flags().StringVar(&key, "key", "", "sign with this ssh private key")
	cmd.Flags().StringVar(&repo, "repo", ".", "the kno checkout to sign or check")
	return cmd
}
