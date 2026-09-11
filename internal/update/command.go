package update

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/track"
)

func pushOnly(e track.Env) bool { return len(e.Services) == 0 && len(e.Managed) > 0 }

type Flags struct {
	Hosts, Group string

	Policy  string
	Dry     bool
	Force   bool
	Verbose bool

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
	return f
}

func organizeGroup(w io.Writer, e track.Env, f Flags) int {
	return RunRemoteGroup(w, e, f.env())
}

func Command() *cobra.Command {
	var v Flags

	cmd := &cobra.Command{
		Use:          "update",
		Short:        "install and verify the target version, one host at a time",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			v.Dry, v.Verbose = cli.DryRun(), cli.Verbose()

			if v.Verbose {
				_ = os.Setenv("V", "1")
			}

			if e, err := track.LoadEnv(); err == nil && pushOnly(e) {
				return cli.Exit(organizeGroup(cmd.ErrOrStderr(), e, v))
			}
			if v.Hosts != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "kno update: --host works only on the organizer;"+
					" this machine runs services and updates itself")
				return cli.Exit(1)
			}
			if v.Dry {
				_ = os.Setenv("DRY_RUN", "1")
			}
			return cli.Exit(Start(cmd.ErrOrStderr(), v.env()))
		},
	}
	f := cmd.Flags()
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
