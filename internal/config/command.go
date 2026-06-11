package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/cli"
)

func Command() *cobra.Command {
	var path, network, host string
	var sh, listNodes, listOps, peers bool

	cmd := &cobra.Command{
		Use:          "config",
		Short:        "print or query the resolved operator config",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
			repo := RepoDir()

			if listNodes {
				net := network
				if net == "" {
					net = "namenlos"
				}
				for _, n := range ListNodes(repo, net) {
					fmt.Fprintln(out, n)
				}
				return nil
			}

			if listOps {
				net := network
				if net == "" {
					net = "namenlos"
				}
				ops := LoadOperators(repo, net)
				names := make([]string, 0, len(ops))
				for n := range ops {
					names = append(names, n)
				}
				sort.Strings(names)
				for _, n := range names {
					fmt.Fprintf(out, "%s\t%s\n", n, strings.Join(ops[n], " "))
				}
				return nil
			}

			p := ResolvePath(path)
			if p == "" {

				if sh {
					return &cli.ExitError{Code: 3}
				}
				return cli.Failf(errOut, 3,
					"no kno.toml found (--config, $KNO_CONFIG, ~/.local/katzenpost/kno.toml, /etc/katzenpost/kno.toml)")
			}
			c, err := Load(p)
			if err != nil {
				return cli.Failf(errOut, 1, "kno config: %v", err)
			}

			if sh {
				if host != "" {
					if _, ok := c.Hosts[host]; !ok {
						return cli.Failf(errOut, 1, "kno config: no host %q in %s", host, p)
					}
				}
				emitSh(c, repo, host)
				return nil
			}

			if peers {
				names := make([]string, 0, len(c.Peers))
				for n := range c.Peers {
					names = append(names, n)
				}
				sort.Strings(names)
				for _, n := range names {
					fmt.Fprintf(out, "%s\t%s\n", n, c.Peers[n])
				}
				return nil
			}

			Summary(out, p, c, repo)

			fmt.Fprintf(out, "\nThis is the config. For versions, releases and"+
				" what each host is running: kno status\n")
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(&path, "config", "", "explicit config file (highest precedence)")
	f.StringVar(&network, "network", "", "network name (for --list-nodes; default namenlos)")
	f.BoolVar(&sh, "sh", false, "emit shell KEY=VALUE for the resolved config")
	f.BoolVar(&listNodes, "list-nodes", false, "list selectable nodes for the network")
	f.BoolVar(&listOps, "operators", false, "list operator groups as: name<TAB>node node ...")
	f.StringVar(&host, "host", "", "emit for this host label instead of the local host")
	f.BoolVar(&peers, "peers", false, "list peer address overrides as: name<TAB>address")
	return cmd
}
