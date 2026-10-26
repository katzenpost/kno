package setup

import (
	"fmt"

	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	var out string
	var system bool
	cmd := &cobra.Command{
		Use:          "setup",
		Short:        "write the kno.toml of this host",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			if system {
				if out != "" {
					return fmt.Errorf("--system and --out name different files. Pass one")
				}
				out = systemConfigPath
			}
			run(out)
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "output file (default ~/.local/katzenpost/kno.toml)")
	cmd.Flags().BoolVar(&system, "system", false,
		"write "+systemConfigPath+" instead, which is the one a node's services and the updater read (needs root)")
	return cmd
}
