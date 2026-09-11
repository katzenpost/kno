package clean

import (
	"context"
	"fmt"

	"github.com/katzenpost/kno/internal/sshx"

	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/hostsel"
	"github.com/katzenpost/kno/internal/track"
)

func Command() *cobra.Command {
	var k Kind
	var all, hosts bool
	var sel hostsel.Flags

	cmd := &cobra.Command{
		Use:          "clean",
		Short:        "show what kno keeps here, and remove what kno can make again",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := cmd.OutOrStdout()
			e, err := track.LoadEnv()
			if err != nil {
				return err
			}
			items := sorted(Survey(e))

			if all {
				k = Kind{Cache: true, Releases: true, Images: true}
			}
			fmt.Fprintln(w, "== kno on this machine ==")
			Report(w, items)

			if sel.Chose() {
				hosts = true
			}
			if hosts {
				labels, err := hostsel.Labels(e, sel)
				if err != nil {
					return err
				}
				fmt.Fprintln(w, "\n== releases held on the hosts this machine organizes ==")
				plans := PlanHosts(e, labels, sshx.Program())
				ReportHosts(w, plans)
				if err := RemoveHosts(w, plans, sshx.Program(), cli.DryRun()); err != nil {
					return err
				}
				if cli.DryRun() {
					fmt.Fprintln(w, "\n--dry-run: nothing was removed.")
				}
				if !k.Any() {
					return nil
				}
			}

			if !k.Any() {
				fmt.Fprintln(w, "\nnothing removed. --cache, --releases, --images, --all"+
					" or --hosts; add --dry-run to see what each would take.")
				return nil
			}
			fmt.Fprintln(w)
			if err := Remove(w, e, Removable(items, k), cli.DryRun()); err != nil {
				return err
			}
			if k.Images {
				if err := PruneImages(context.Background(), w, compile.Podman(), cli.DryRun()); err != nil {
					fmt.Fprintf(w, "  images: %v\n", err)
				}
			}
			if cli.DryRun() {
				fmt.Fprintln(w, "\n--dry-run: nothing was removed.")
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVar(&k.Cache, "cache", false, "remove the build caches and the FreeBSD sysroot")
	f.BoolVar(&k.Releases, "releases", false, "remove built release trees")
	f.BoolVar(&k.Images, "images", false, "reclaim unreferenced container layers")
	f.BoolVar(&all, "all", false, "all three")
	f.BoolVar(&hosts, "hosts", false,
		"remove the releases piled up on the hosts this machine organizes, over ssh")
	hostsel.Bind(cmd, &sel)
	return cmd
}
