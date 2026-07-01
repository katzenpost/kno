package freebsd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/source"
)

func SysrootCommand() *cobra.Command {
	return &cobra.Command{
		Use:          "sysroot [dir]",
		Short:        "fetch the pinned FreeBSD base system",
		Args:         cli.Args(cobra.MaximumNArgs(1)),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			pins, err := source.ParsePins(source.PinsPath())
			if err != nil {
				return cli.Failf(cmd.ErrOrStderr(), 1, "kno build sysroot: %v", err)
			}
			var out string
			if len(args) == 1 {
				out, err = Sysroot(args[0], pins)
			} else {
				out, err = SysrootTarball(pins)
			}
			if err != nil {
				return cli.Failf(cmd.ErrOrStderr(), 1, "kno build sysroot: %v", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		},
	}
}

func VMImageCommand() *cobra.Command {
	return &cobra.Command{
		Use:          "vmimage [path]",
		Short:        "fetch the pinned FreeBSD VM image",
		Args:         cli.Args(cobra.MaximumNArgs(1)),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			pins, err := source.ParsePins(source.PinsPath())
			if err != nil {
				return cli.Failf(cmd.ErrOrStderr(), 1, "kno build vmimage: %v", err)
			}
			dest := ""
			if len(args) == 1 {
				dest = args[0]
			}
			out, err := VMImage(dest, pins)
			if err != nil {
				return cli.Failf(cmd.ErrOrStderr(), 1, "kno build vmimage: %v", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		},
	}
}
