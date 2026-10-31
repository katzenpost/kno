package update

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/build"
	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/track"
)

func pushOnly(e track.Env) bool { return len(e.Services) == 0 && len(e.Managed) > 0 }

type Flags struct {
	Hosts, Group string
	Target       string
	Tag, Ref     string
	Selected     *build.SourceSelection

	AllowDowngrade bool
	Policy         string
	Dry            bool
	Force          bool
	Verbose        bool

	StallForward bool
	Push         bool
	Promote      bool
	NoWait       bool
}

func (f Flags) env() Flags {
	f.Dry = f.Dry || os.Getenv("DRY_RUN") == "1"
	f.StallForward = f.StallForward || os.Getenv("NETWORK_STALL_FORWARD") == "1"
	if f.Policy == "" {
		f.Policy = os.Getenv("KNO_UPDATE_POLICY")
	}
	if f.Target != "" {
		if f.Policy == "" {
			f.Policy = "apply"
		}
		f.Dry = f.Dry || f.Policy == "simulate"
	}
	return f
}

func updateArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("choose one update target: kno or katzenpost")
	}
	if len(args) == 1 && args[0] != "kno" && args[0] != "katzenpost" {
		return fmt.Errorf("unknown update target %q: use kno or katzenpost", args[0])
	}
	tag, _ := cmd.Flags().GetString("tag")
	ref, _ := cmd.Flags().GetString("ref")
	if tag != "" || ref != "" {
		if len(args) == 0 {
			return fmt.Errorf("--tag and --ref need an update target: kno or katzenpost")
		}
		if _, err := track.SourceRef(tag, ref); err != nil {
			return err
		}
	}
	if len(args) == 0 && cmd.Flags().Changed("allow-downgrade") {
		return fmt.Errorf("--allow-downgrade needs an explicit update target")
	}
	if len(args) == 1 && args[0] == "kno" {
		for _, flag := range []string{"host", "group", "force", "push", "stall-forward", "no-wait", "promote"} {
			if cmd.Flags().Changed(flag) {
				return fmt.Errorf("kno update kno updates this host's tool and configuration. --%s applies to node deployments", flag)
			}
		}
	}
	return nil
}

func organizeGroup(w io.Writer, e track.Env, f Flags) int {
	return RunRemoteGroup(w, e, f.env())
}

func Command() *cobra.Command {
	var v Flags

	cmd := &cobra.Command{
		Use:   "update [kno|katzenpost]",
		Short: "install the target version and run kno verify, one host at a time",
		Long: "Update without a target follows the saved update policy.\n" +
			"update kno fetches and installs kno and its host configuration on this machine.\n" +
			"update katzenpost updates Katzenpost and its node configs, keeping kno.\n" +
			"Choose --tag TAG for a tagged release, or --ref main or a commit SHA.\n" +
			"An explicit target applies once. --dry-run and --policy still take precedence.",
		Args:         updateArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			v.Dry, v.Verbose = cli.DryRun(), cli.Verbose()
			if len(args) == 1 {
				v.Target = args[0]
			}
			v = v.env()
			if v.Policy != "" && v.Policy != "off" && v.Policy != "simulate" && v.Policy != "apply" {
				return fmt.Errorf("unknown update policy %q: use off, simulate or apply", v.Policy)
			}

			if v.Verbose {
				_ = os.Setenv("V", "1")
			}
			if v.Target == "kno" {
				return cli.Exit(updateKno(cmd.ErrOrStderr(), v))
			}

			if e, err := track.LoadEnv(); err == nil {
				if pushOnly(e) {
					return cli.Exit(organizeGroup(cmd.ErrOrStderr(), e, v))
				}
			}
			if v.Hosts != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "kno update: --host works only on the organizer."+
					" This host runs services and updates itself")
				return cli.Exit(1)
			}
			if v.Target != "" && v.Group != "" {
				return fmt.Errorf("--group works on the organizer. This host updates itself")
			}
			if v.Dry {
				_ = os.Setenv("DRY_RUN", "1")
			}
			return cli.Exit(Start(cmd.ErrOrStderr(), v.env()))
		},
	}
	f := cmd.Flags()
	f.StringVar(&v.Tag, "tag", "", "install this target's tagged source release")
	f.StringVar(&v.Ref, "ref", "", "install this target's main branch or exact commit SHA")
	f.BoolVar(&v.AllowDowngrade, "allow-downgrade", false, "allow an explicitly selected older target. Keep preflight validation")
	f.StringVar(&v.Hosts, "host", "", "update these hosts (comma separated labels from kno.toml)")
	f.StringVar(&v.Group, "group", "",
		"update every host carrying an operator's nodes (the operator in machines/<node>.toml)")
	f.StringVar(&v.Policy, "policy", "",
		"override [update].policy for this run: off, simulate or apply")
	f.BoolVarP(&v.Force, "force", "f", false,
		"install the target again: stop the verify window, skip the state preflight, reinstall a tag the host has")
	f.BoolVar(&v.Push, "push", false,
		"push the deployment records to the shared repository")
	f.BoolVar(&v.StallForward, "stall-forward", false,
		"hold a release rather than roll it back when the network itself has no consensus")
	f.BoolVar(&v.NoWait, "no-wait", false,
		"do one step for each host and return, instead of waiting until each host is promoted or rolled back")
	f.BoolVar(&v.Promote, "promote", false,
		"mark a host as last good when verify passes, without the 2-epoch (40 min) wait")
	return cmd
}

func selectUpdateSource(out io.Writer, e track.Env, v *Flags) error {
	if v.Selected != nil || (v.Target != "katzenpost" && v.Tag == "" && v.Ref == "") {
		return nil
	}
	if v.Tag == "" && v.Ref == "" {
		cache := filepath.Join(e.StateDir, "katzenpost-source")
		if _, err := track.ResolveSource(track.SourceRequest{Dir: cache, URL: e.KatzenpostURL, Ref: "main", Refresh: true}, track.Logger{Tool: "update", W: out}); err != nil {
			return err
		}
		policy := e
		policy.KatzenpostRepo = cache
		var why string
		v.Tag, why = policy.Target()
		if v.Tag == "" {
			return fmt.Errorf("%s", noTarget(why))
		}
		if why != "" {
			fmt.Fprintln(out, why)
		}
	}
	var err error
	v.Selected, err = build.SelectKatzenpost(e, v.Tag, v.Ref, track.Logger{Tool: "update", W: out})
	return err
}

func drySelectedUpdate(out io.Writer, v Flags) bool {
	if (!v.Dry && v.Policy != "simulate" && os.Getenv("DRY_RUN") != "1") || (v.Target == "" && v.Tag == "" && v.Ref == "") {
		return false
	}
	selector := v.Tag + v.Ref
	if selector == "" {
		selector = "the release policy target"
	}
	fmt.Fprintf(out, "DRYRUN: select %s once, then build and verify its packages. No source fetched or package installed.\n", selector)
	return true
}

func selectedDowngrade(selected *build.SourceSelection, installed string, allow bool) error {
	if selected != nil && installed != "" && !allow && deb.CompareVersions(strings.TrimPrefix(selected.Tag, "v"), strings.TrimPrefix(installed, "v")) < 0 {
		return fmt.Errorf("installed Katzenpost %s is newer than selected %s. Use --allow-downgrade to allow the downgrade", installed, selected.Tag)
	}
	return nil
}
