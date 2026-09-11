package verify

import (
	"strconv"

	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/hostsel"
	"github.com/katzenpost/kno/internal/service"
	"github.com/katzenpost/kno/internal/track"
)

var flagEnv = []struct{ flag, env string }{
	{"consensus-only", "VERIFY_CONSENSUS_ONLY"},
	{"participation", participationEnv},
	{"dataplane", "VERIFY_DATAPLANE"},
	{"pigeonhole", "VERIFY_PIGEONHOLE"},
	{"generic", "VERIFY_GENERIC"},
	{"thinclient", "VERIFY_THINCLIENT"},
	{"min-replicas", "VERIFY_MIN_REPLICAS"},
	{"fetch-timeout", "VERIFY_FETCH_TIMEOUT"},
	{"max-restarts", "VERIFY_MAX_RESTARTS"},
	{"since", "VERIFY_SINCE"},
}

func changedFlags(cmd *cobra.Command) Flags {
	f := Flags{}
	for _, m := range flagEnv {
		fl := cmd.Flags().Lookup(m.flag)
		if fl == nil || !fl.Changed {
			continue
		}
		v := fl.Value.String()
		if fl.Value.Type() == "bool" {
			v = "0"
			if fl.Value.String() == "true" {
				v = "1"
			}
		}
		f[m.env] = v
	}
	return f
}

func Command() *cobra.Command {
	var sel hostsel.Flags

	cmd := &cobra.Command{
		Use:          "verify [release-dir]",
		Short:        "verify a node and consensus after an update",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			errOut := cmd.ErrOrStderr()
			vf := changedFlags(cmd)
			if sel.Chose() {
				return cli.Exit(RunHosts(errOut, sel, vf, args))
			}
			o, err := OptionsFor(args, vf)
			if err != nil {

				return cli.Failf(errOut, CodeNotYet, "%v", err)
			}
			if sel.Verbose() {
				track.Logger{Tool: "verify", W: errOut}.Logf("checking: %s", Enabled(o))
			}
			return cli.Exit(Verify(errOut, o))
		},
	}
	cmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return cli.Failf(c.ErrOrStderr(), CodeNotYet, "Error: %v", err)
	})
	hostsel.Bind(cmd, &sel)
	f := cmd.Flags()
	f.Bool("consensus-only", false,
		"ask only whether a consensus is present; skip the unit and data-plane checks")
	f.Bool("participation", true,
		"require this node in the consensus, as kno update does (--participation=false turns it off)")
	f.Bool("dataplane", false,
		"also send a test message from this host through the network and wait for the reply (echo ping)")
	f.Bool("pigeonhole", false,
		"with --dataplane, also run a pigeonhole round trip")
	f.Bool("generic", false,
		"verify a node this repository does not describe (needs KNO_FETCH)")
	f.String("thinclient", "",
		"the thin-client config that gives the kpclientd socket address (default "+service.Thinclient+")")
	f.String("min-replicas", "",
		"require this many storage replicas ready before answering")
	f.String("fetch-timeout", "",
		"how long to wait for a consensus, in seconds (default 120)")
	f.String("max-restarts", "",
		"the most restarts that a unit can have and pass (default "+strconv.Itoa(DefaultMaxRestarts)+")")
	f.String("since", "",
		"the start time of the kpclientd log search (default "+defaultSince+")")
	return cmd
}
