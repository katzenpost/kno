package nodeedit

import (
	"bufio"
	"fmt"
	"io"
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
	var network, node, addresses, keys string
	var list bool

	cmd := &cobra.Command{
		Use:          "edit",
		Short:        "change the addresses or the keys of your node",
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
			if cmd.Flags().Changed("keys") {
				if cli.DryRun() {
					fmt.Fprintf(w, "--dry-run: would put the keys of %s"+
						" into the repo\n", n.File)
					return nil
				}
				if keys == "dataDir" {
					keys = ""
				}
				if err := ImportKeys(repo, network, n, keys); err != nil {
					return err
				}
				fmt.Fprintf(w, "wrote the new keys of %s\n", n.File)
				return rendered(w, repo, network)
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
				fmt.Fprintf(w, "%s already announces that. Nothing written\n",
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
			return rendered(w, repo, network)
		},
	}
	f := cmd.Flags()
	f.StringVar(&network, "network", "", "network name (default: this config's)")
	f.StringVar(&node, "node", "", "which of your nodes (default: the only one)")
	f.StringVar(&addresses, "addresses", "",
		"set these addresses instead of asking, space separated")
	f.BoolVar(&list, "list", false, "show your nodes and their addresses")
	f.StringVar(&keys, "keys", "", "put the node's new public keys into the"+
		" repo, from this directory")
	f.Lookup("keys").NoOptDefVal = "dataDir"
	return cmd
}

func rendered(w io.Writer, repo, network string) error {
	err := render.Generate(knopath.NetworkDir(repo, network), "")
	if err != nil {
		return fmt.Errorf("rendering the configs: %w", err)
	}
	fmt.Fprintln(w, "next: kno contribute")
	return nil
}
