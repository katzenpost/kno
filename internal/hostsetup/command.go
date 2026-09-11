package hostsetup

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/config"
)

func DepsCommand() *cobra.Command {
	var manager string

	cmd := &cobra.Command{
		Use:          "deps [profile]",
		Short:        "install the programs that kno needs",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()

			m := manager
			if m == "" {
				d, err := DetectManager()
				if err != nil {
					return cli.Failf(errOut, 1, "kno deps: %v", err)
				}
				m = d
			}
			profile := ProfileFull
			if len(args) > 0 && args[0] != "" {
				profile = args[0]
			}
			plan, err := DepsPlan(DepsOptions{Profile: profile, Manager: m, Packages: EnvPackages(m)})
			if err != nil {
				return cli.Failf(errOut, 1, "kno deps: %v", err)
			}
			if cli.DryRun() {
				for _, l := range plan.Lines() {
					fmt.Fprintln(out, l)
				}
				return nil
			}
			if os.Geteuid() != 0 {
				return cli.Failf(errOut, 1, "kno deps: must run as root (or use --dry-run)")
			}
			if err := plan.Apply(context.Background(), errOut); err != nil {
				return cli.Failf(errOut, 1, "kno deps: %v", err)
			}
			fmt.Fprintf(errOut, "kno deps: dependencies installed (%s)\n", profile)
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(&manager, "manager", "", "package manager to drive (default: this host's)")
	return cmd
}

func BootstrapCommand() *cobra.Command {
	var rev string
	var pkg bool

	cmd := &cobra.Command{
		Use:          "bootstrap",
		Short:        "set up an updater user, repo and hooks on a host",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()

			if err := bootstrapSupported(exec.LookPath); err != nil {
				return cli.Failf(errOut, 1, "kno bootstrap: %v", err)
			}
			repo := config.RepoDir()
			if !pkg {
				if err := checkRevision(repo, rev); err != nil {
					return cli.Failf(errOut, 1, "kno bootstrap: %v", err)
				}
			}
			o := EnvBootstrapOptions(repo)
			o.PackageInstall = pkg
			if cli.DryRun() {
				show := func(p Plan) error {
					for _, l := range p.Lines() {
						fmt.Fprintln(out, l)
					}
					return nil
				}
				plan, _, err := bootstrap(o, Probe, show)
				if err != nil {
					return cli.Failf(errOut, 1, "kno bootstrap: %v", err)
				}
				return show(plan)
			}
			if os.Geteuid() != 0 {
				return cli.Failf(errOut, 1, "kno bootstrap: must run as root (or use --dry-run)")
			}
			apply := func(p Plan) error { return p.Apply(context.Background(), errOut) }
			plan, f, err := bootstrap(o, Probe, apply)
			if err != nil {
				return cli.Failf(errOut, 1, "kno bootstrap: %v", err)
			}
			if o.OperatorPubkey == "" && !pkg {
				fmt.Fprintf(errOut,
					"kno bootstrap: no OPERATOR_PUBKEY set; authorize the operator's ssh key to push to %s\n",
					o.Bare)
			}
			if err := apply(plan); err != nil {
				return cli.Failf(errOut, 1, "kno bootstrap: %v", err)
			}
			if pkg {

				priv := "no path to root"
				if (config.Host{Privilege: o.Privilege}).WritesSudoers() {
					priv = SudoersPath
				}
				fmt.Fprintf(errOut, "kno bootstrap: host prepared (updater user %s, %s, systemd units)\n",
					o.User, priv)
				return nil
			}
			fmt.Fprintln(errOut, nextSteps(o, f))
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(&rev, "rev", "", "refuse to run unless the repo is at this revision")
	f.BoolVar(&pkg, "package", false,
		"the subset a .deb postinst may do: user, dirs, sudoers and units; no clone, no bare repo, no timer start")
	return cmd
}

func bootstrap(o BootstrapOptions, probe func(BootstrapOptions) Facts, run func(Plan) error) (Plan, Facts, error) {
	if err := o.check(); err != nil {
		return nil, Facts{}, err
	}
	if o.Deps != nil && !o.PackageInstall {
		deps, err := DepsPlan(*o.Deps)
		if err != nil {
			return nil, Facts{}, err
		}
		for i := range deps {
			deps[i].BestEffort = true
		}
		if err := run(deps); err != nil {
			return nil, Facts{}, err
		}
	}
	f := probe(o)
	p, err := BootstrapPlan(o, f)
	return p, f, err
}

func nextSteps(o BootstrapOptions, f Facts) string {
	conf := "install the kno-host-config package, or run 'kno setup --system', to write " + SystemConfPath
	if f.GitShell == "" {
		return "kno bootstrap: complete. This host has no git-shell, so it has no bare repo" +
			" to push to. Next: " + conf + "."
	}
	return "kno bootstrap: complete. Next: git push <node>:" + o.Bare + " --tags. Then " + conf + "."
}

func bootstrapSupported(look func(string) (string, error)) error {
	if _, err := look("useradd"); err != nil {
		return fmt.Errorf("this machine has no useradd. kno bootstrap sets up a Debian node:" +
			" an updater account, a sudoers rule and systemd units. A FreeBSD node gets its" +
			" setup from its kno-host-config package and runs kno update as root from cron," +
			" and an operator machine needs no bootstrap")
	}
	return nil
}
