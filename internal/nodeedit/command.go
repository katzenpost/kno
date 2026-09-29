package nodeedit

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/nodefile"
	"github.com/katzenpost/kno/internal/render"
)

func Command() *cobra.Command {
	var network, node, addresses string
	var list bool

	cmd := &cobra.Command{
		Use:          "edit",
		Short:        "change the addresses of your node",
		Args:         cli.Args(cobra.NoArgs),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo := config.RepoDir()
			c, err := config.Load(config.ResolvePath(""))
			if err != nil {
				return fmt.Errorf("reading your config: %w", err)
			}
			if network == "" {
				network = config.NetworkOf(c)
			}
			w := cmd.OutOrStdout()
			mine := Mine(repo, network, c.Nodes)
			if list {
				Report(w, mine)
				return nil
			}
			n, err := Pick(mine, node)
			if err != nil {
				return err
			}
			now, err := nodefile.Addresses(n.Path)
			if err != nil {
				return err
			}
			want := addresses
			if want == "" {
				if cli.DryRun() {
					fmt.Fprintf(w, "--dry-run: would ask for %s's addresses,"+
						" which are %s\n", n.File, strings.Join(now, " "))
					return nil
				}
				fmt.Fprintf(w, "%s currently announces:\n", n.File)
				for _, a := range now {
					fmt.Fprintf(w, "  %s\n", a)
				}
				fmt.Fprintln(w, "Give every address the node announces,"+
					" separated by a space.")
				r := bufio.NewReader(os.Stdin)
				fmt.Fprintf(os.Stderr, "addresses [%s]: ", strings.Join(now, " "))
				line, _ := r.ReadString('\n')
				want = strings.TrimSpace(line)
				if want == "" {
					want = strings.Join(now, " ")
				}
			}
			addrs, err := ParseAddresses(want)
			if err != nil {
				return err
			}
			if strings.Join(addrs, " ") == strings.Join(now, " ") {
				fmt.Fprintf(w, "%s already announces that; nothing written\n",
					n.File)
				return nil
			}
			if cli.DryRun() {
				fmt.Fprintf(w, "--dry-run: would set %s to %s\n", n.File,
					strings.Join(addrs, " "))
				return nil
			}
			if err := nodefile.SetAddresses(n.Path, addrs); err != nil {
				return err
			}
			fmt.Fprintf(w, "wrote %s\n", n.File)
			for _, a := range addrs {
				fmt.Fprintf(w, "  %s\n", a)
			}
			if err := render.Generate(knopath.NetworkDir(repo, network), ""); err != nil {
				return fmt.Errorf("rendering the configs: %w", err)
			}
			fmt.Fprintln(w, "next: kno contribute")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&network, "network", "", "network name (default: this config's)")
	f.StringVar(&node, "node", "", "which of your nodes (default: the only one)")
	f.StringVar(&addresses, "addresses", "",
		"set these addresses instead of asking, space separated")
	f.BoolVar(&list, "list", false, "show your nodes and their addresses")
	return cmd
}
