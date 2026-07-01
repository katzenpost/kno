package check

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/config"
)

var checks = [][2]string{
	{"render", "every network renders to its committed configs"},
	{"operator", "every operator group's flow resolves"},
	{"topology", "the network data's own consistency lint"},
	{"repro", "build each artifact kind twice and compare"},
	{"all", "render, operator and topology"},
}

func Command() *cobra.Command {
	var repo, network string
	var toolchain, plan, allowSkip bool

	names := make([]string, 0, len(checks))
	for _, c := range checks {
		names = append(names, c[0])
	}

	cmd := &cobra.Command{
		Use:          "check <what>",
		Short:        "run the repository checks",
		ValidArgs:    names,
		Args:         cli.Args(cobra.MaximumNArgs(1)),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
			what := ""
			if len(args) > 0 {
				what = args[0]
			}

			if repo == "" {
				repo = config.RepoDir()
			}
			if network == "" {
				network = os.Getenv("KNO_NETWORK")
			}

			topoNetwork := network
			if network == "" {
				network = "namenlos"
			}

			var res Result
			var err error
			switch what {
			case "render":
				res, err = Render(RenderOptions{Repo: repo})
			case "operator":
				res, err = Operator(OperatorOptions{Repo: repo, Network: network})
			case "topology":
				res, err = Topology(TopologyOptions{Repo: repo, Network: topoNetwork})
			case "repro":
				ro := ReproOptions{Repo: repo, Toolchain: toolchain, AllowSkip: allowSkip}
				if plan {
					printPlan(out, ReproSteps(ro))
					return nil
				}
				res, err = Repro(context.Background(), ro)
			case "all":
				res, err = All(repo, network)
			default:
				usage(errOut, cmd.CommandPath())
				return &cli.ExitError{Code: 2}
			}
			if err != nil {
				return cli.Failf(errOut, 2, "kno check: %v", err)
			}

			Report(out, res)
			if os.Getenv("GITHUB_ACTIONS") != "" {
				annotate(out, os.Getenv("GITHUB_STEP_SUMMARY"), res)
			}
			if res.Failed() {
				return &cli.ExitError{Code: 1}
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(&repo, "repo", "", "network repo (default $KNO_REPO, else the working directory)")
	f.StringVar(&network, "network", "", "network name (default $KNO_NETWORK, else namenlos)")
	f.BoolVar(&toolchain, "toolchain", false, "repro: also rebuild the vendored toolchain (needs network)")
	f.BoolVar(&plan, "plan", false, "repro: print what would be built twice, and build nothing")
	f.BoolVar(&allowSkip, "allow-skip", false, "repro: accept a leg whose prerequisite is missing instead of failing")
	return cmd
}

func usage(w io.Writer, path string) {
	fmt.Fprintf(w, "usage: %s <what> [flags]\n", path)
	for _, c := range checks {
		fmt.Fprintf(w, "  %-12s %s\n", c[0], c[1])
	}
}
