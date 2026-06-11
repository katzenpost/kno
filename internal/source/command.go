package source

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/config"
)

func PinsPath() string { return filepath.Join(config.RepoDir(), "thirdparty", "pins.env") }

func PinCommand() *cobra.Command {
	return &cobra.Command{
		Use:          "pin [" + strings.Join(PinModes, "|") + "]",
		Short:        "point pins.env at a katzenpost ref",
		ValidArgs:    PinModes,
		Args:         cli.Args(cobra.MaximumNArgs(1)),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			mode := PinTag
			if len(args) == 1 {
				mode = args[0]
			}
			path := PinsPath()
			ref, err := Repin(path, mode)
			if err != nil {
				return cli.Failf(cmd.ErrOrStderr(), 1, "kno build pin: %v", err)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "kno build pin: %s KATZENPOST_REF=%s (mode=%s)\n",
				path, ref, mode)
			fmt.Fprintln(cmd.OutOrStdout(), ref)
			return nil
		},
	}
}
