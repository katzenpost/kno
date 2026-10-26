package contribute

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/check"
	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/render"
	"github.com/katzenpost/kno/internal/track"
)

func Command() *cobra.Command {
	var network, branch string
	var apply, push, merge, noRender, noRecord bool

	cmd := &cobra.Command{
		Use:          "contribute",
		Short:        "put your node changes on a branch to merge into kno",
		Args:         cli.Args(cobra.NoArgs),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo := checkoutRepo()
			if repo == "" {
				return fmt.Errorf("cannot locate the network repo (set KNO_REPO)." +
					"\n  A contribution needs a checkout: the copy compiled into" +
					" kno has no branches.")
			}
			c, err := config.Load(config.ResolvePath(""))
			if err != nil {
				return fmt.Errorf("reading your config: %w", err)
			}
			if network == "" {
				network = config.NetworkOf(c)
			}
			w := cmd.OutOrStdout()
			dry := cli.DryRun()
			rendered := !noRender && !dry
			if rendered {
				if err := render.Generate(
					knopath.NetworkDir(repo, network), ""); err != nil {
					return fmt.Errorf("rendering the node configs: %w", err)
				}
			}

			var res check.Result
			if rendered || noRender {
				var err error
				res, err = check.All(repo, network)
				if err != nil {
					return fmt.Errorf("running the repository checks: %w", err)
				}
			}
			who := operatorOf(repo, network, c)
			o := Options{Repo: repo, Network: network, Nodes: c.Nodes,
				Known: config.ListNodes(repo, network), Branch: branch}
			if o.Branch == "" {
				o.Branch = defaultBranch(who, c.Nodes, time.Now())
			}
			p, err := Collect(o)
			if err != nil {
				return err
			}
			Report(w, p)
			if dry && !noRender {
				fmt.Fprintln(w, "  --dry-run: the render and the repository"+
					" checks were not run. They need to write")
			}
			if res.Failed() {

				for _, f := range res.Findings {
					if f.Status == check.Fail {
						fmt.Fprintf(w, "  %s\n", f)
					}
				}
				return cli.Failf(cmd.ErrOrStderr(), 1,
					"kno contribute: the repository checks fail, so this would"+
						" open a pull request CI rejects.\n"+
						"  'kno check all' shows them all. --no-render if a"+
						" render is not what is wrong.")
			}
			if !apply || p.Empty() {
				return nil
			}
			if dry {
				fmt.Fprintf(w, "\n--dry-run: would create %s and commit"+
					" %d file(s)\n", p.Branch, len(p.Mine)+len(p.Others)+len(p.SharedChanges))
				return nil
			}
			if err := Apply(p, Message(p, who)); err != nil {
				return err
			}
			fmt.Fprintf(w, "\ncommitted on %s\n", p.Branch)
			if !noRecord {
				fmt.Fprintf(w, "building and recording the config packages of %s\n",
					strings.Join(p.Nodes(), " "))
				wrote, err := RecordNodes(p, repo, network, c)
				var waits RecordWaits
				switch {
				case errors.As(err, &waits):
					fmt.Fprintf(w, "%v\n", waits)
				case err != nil:
					return fmt.Errorf("the change is on %s, but kno could not record its"+
						" config packages: %w\n  To merge it without the record, run: git merge"+
						" --no-ff %s. Then run 'kno build' and 'kno build record'", p.Branch, err, p.Branch)
				case len(wrote) > 0:
					fmt.Fprintf(w, "recorded %d file(s) on %s\n", len(wrote), p.Branch)
				}
			}
			ship, note := p.Branch, "open a pull request into "+p.Base
			if merge {
				if err := Merge(p); err != nil {
					return err
				}
				ship, note = p.Base, "one merge commit on "+p.Base
				fmt.Fprintf(w, "merged into %s as one merge commit\n", p.Base)
			}
			if !push {
				fmt.Fprintf(w, "  git -C %s push -u origin %s\n", p.Root, ship)
				fmt.Fprintf(w, "  then: %s\n", note)
				return nil
			}
			if err := PushBranch(p.Root, ship); err != nil {
				return err
			}
			fmt.Fprintf(w, "pushed %s: %s\n", ship, note)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&network, "network", "", "network name (default: this config's)")
	f.StringVar(&branch, "branch", "", "branch to create (default: your handle and the date)")
	f.BoolVar(&apply, "apply", false, "create the branch and commit. Without it, print the plan")
	f.BoolVar(&push, "push", false, "with --apply, also push to origin")
	f.BoolVar(&merge, "merge", false,
		"with --apply, merge the branch into the branch you were on, as one"+
			" merge commit")
	f.BoolVar(&noRender, "no-render", false, "do not render the node configs first")
	f.BoolVar(&noRecord, "no-record", false,
		"do not build and record the config packages of the changed nodes")
	return cmd
}

func Report(w io.Writer, p Plan) {
	if p.Empty() {
		fmt.Fprintln(w, "kno contribute: no node has changed in this checkout")
	} else {
		fmt.Fprintf(w, "kno contribute: %s would carry\n", p.Branch)
		for _, c := range p.Mine {
			fmt.Fprintf(w, "  %-58s %s\n", c.Path, c.Node)
		}
		for _, c := range p.Others {
			fmt.Fprintf(w, "  %-58s %s (not a node you claim)\n", c.Path, c.Node)
		}
		for _, c := range p.SharedChanges {
			fmt.Fprintf(w, "  %-58s shared\n", c.Path)
			if len(c.Mentions) > 0 {
				fmt.Fprintf(w, "    review: the changed lines also name %v\n",
					c.Mentions)
			}
		}
	}
	for _, t := range p.Theirs {
		fmt.Fprintf(w, "  not a node's file, left alone: %s\n", t)
	}
	for _, s := range p.Skipped {
		fmt.Fprintf(w, "  not carried: %s\n", s)
	}
}

func defaultBranch(who string, nodes []string, now time.Time) string {
	if who == "" && len(nodes) > 0 {
		who = nodes[0]
	}
	if who == "" {
		who = "nodes"
	}
	return fmt.Sprintf("%s-nodes-%s", who, now.Format("20060102"))
}

func operatorOf(repo, network string, c config.Config) string {
	claimed := map[string]bool{}
	for _, n := range c.Nodes {
		claimed[n] = true
	}
	ops := config.LoadOperators(repo, network)
	handles := make([]string, 0, len(ops))
	for h := range ops {
		handles = append(handles, h)
	}
	sort.Strings(handles)
	for _, handle := range handles {
		for _, n := range ops[handle] {
			if claimed[n] {
				return handle
			}
		}
	}
	return ""
}

func checkoutRepo() string {
	if r := os.Getenv("KNO_REPO"); r != "" {
		if d, ok := config.DataRootOf(r); ok {
			return d
		}
		return r
	}
	if d, ok := config.CheckoutDir(); ok {
		return d
	}
	home := os.Getenv("KNO_HOME")
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home != "" {
		if d, ok := config.DataRootOf(track.CloneDir(home)); ok {
			return d
		}
	}
	return config.RepoDir()
}
