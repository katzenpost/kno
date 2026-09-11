package probe

import (
	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/track"
)

func Command() *cobra.Command {
	var configs, thinclient, timeout, fetch string

	cmd := &cobra.Command{
		Use:          "probe",
		Short:        "check the reachability and the consensus of a network",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			errOut := cmd.ErrOrStderr()
			o, err := OptionsFromEnv(configs, thinclient, timeout, fetch)
			if err != nil {
				track.Logger{Tool: "probe", W: errOut}.Logf("%v", err)
				return &cli.ExitError{Code: 2}
			}
			return cli.Exit(Probe(errOut, o))
		},
	}

	f := cmd.Flags()
	f.StringVar(&configs, "configs", "", "directory of server configs to scan (default: $PROBE_CONFIGS)")
	f.StringVar(&thinclient, "thinclient", "", "thin-client config for the consensus fetch (default: $VERIFY_THINCLIENT)")
	f.StringVar(&timeout, "timeout", "", "connect timeout in seconds (default: $PROBE_TIMEOUT, else 5)")
	f.StringVar(&fetch, "fetch", "", "consensus fetch tool (default: $KNO_FETCH, else kpfetch or pq-katzenpost-fetch)")
	return cmd
}
