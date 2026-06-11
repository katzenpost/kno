package render

import (
	"path/filepath"
	"strings"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/cli"
)

func Command() *cobra.Command {
	var dir, out string
	cmd := &cobra.Command{
		Use:          "render",
		Short:        "render node configs from the network repo",
		Args:         cobra.NoArgs,
		SilenceUsage: true,

		RunE: func(cmd *cobra.Command, _ []string) error {
			run := func() error { return Generate(resolveNetwork(dir), "") }
			if out != "" {
				run = func() error { return Render(resolveNetwork(dir), out) }
			}
			if err := run(); err != nil {
				return cli.Failf(cmd.ErrOrStderr(), 1, "kno-render: %v", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "network", "",
		"network name, or a directory holding manifest.toml and nodes/"+
			" (default: namenlos)")
	cmd.Flags().StringVar(&out, "out", "",
		"output dir (default: <network>/.generated/configs)")
	return cmd
}

func resolveNetwork(flag string) string {
	if flag == "" {
		flag = "namenlos"
	}
	if strings.ContainsRune(flag, filepath.Separator) {
		return flag
	}
	return knopath.NetworkDir(config.RepoDir(), flag)
}
