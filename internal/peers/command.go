package peers

import (
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/config"
)

func Command() *cobra.Command {
	var network, node string
	cmd := &cobra.Command{
		Use:          "peers --node <name>",
		Short:        "print the LAN peer addresses of a node",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.OutOrStdout(), network, node)
		},
	}
	cmd.Flags().StringVar(&network, "network", "namenlos", "network name")
	cmd.Flags().StringVar(&node, "node", "", "the node whose peers to resolve")
	_ = cmd.MarkFlagRequired("node")
	return cmd
}

func run(w io.Writer, network, node string) error {
	repo := config.RepoDir()
	m := NodePeers(repo, network, node)
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, p := range names {
		fmt.Fprintf(w, "%s\t%s\n", p, m[p])
	}
	return nil
}
