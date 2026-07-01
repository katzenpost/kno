package knodeb

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/config"
)

func Command() *cobra.Command {
	var version, arch, outdir string

	cmd := &cobra.Command{
		Use:          "knodeb",
		Short:        "build the kno package itself",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			errOut := cmd.ErrOrStderr()

			repo := config.RepoDir()
			o, err := DefaultOptions(repo)
			if err != nil {
				return cli.Failf(errOut, 1, "kno knodeb: %v", err)
			}
			if version != "" {
				o.Version = version
			}
			if arch != "" {
				o.Arch = arch
			}
			if outdir != "" {
				o.OutDir = outdir
			}
			out, err := Package(context.Background(), o)
			if err != nil {
				return cli.Failf(errOut, 1, "kno knodeb: %v", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(&version, "version", "", "package version (default: KATZENPOST_REF from pins.env)")
	f.StringVar(&arch, "arch", "", "Debian architecture (default: this host's)")
	f.StringVar(&outdir, "out", "", "output directory (default: <repo>/dist/kno)")
	return cmd
}
