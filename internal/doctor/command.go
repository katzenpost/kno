package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/hostsel"
	"github.com/katzenpost/kno/internal/track"
	"github.com/katzenpost/kno/internal/verify"
)

func Command() *cobra.Command {
	var fix bool
	var sel hostsel.Flags

	cmd := &cobra.Command{
		Use:          "doctor",
		Short:        "check the health of this host, or of the hosts you organize",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if sel.Chose() {
				return cli.Exit(RunRemoteHosts(cmd.OutOrStdout(), sel, fix, cli.DryRun()))
			}
			if fix {
				return runFix(cmd.OutOrStdout(), cli.DryRun())
			}
			return cli.Exit(Run(cmd.OutOrStdout(), sel.Verbose()))
		},
	}
	hostsel.Bind(cmd, &sel)
	cmd.Flags().BoolVar(&fix, "fix", false,
		"apply the repairs for what doctor finds on this host (needs root)")

	cmd.AddCommand(packageCommand(), toolsCommand())
	return cmd
}

func runFix(w io.Writer, dry bool) error {
	e, err := track.LoadEnv()
	if err != nil {
		return fmt.Errorf("config: %w (fix your kno.toml, or unset KNO_CONFIG)", err)
	}
	return ApplyFixes(context.Background(), w, e, gatherHost(e, verify.Platform()), dry)
}

func Run(w io.Writer, verbose bool) int {
	r := newResult(w, verbose)

	r.line("kno-doctor: kno %s on %s/%s", cli.Version(), runtime.GOOS, runtime.GOARCH)

	e, err := track.LoadEnv()
	if err != nil {

		r.check("config", false, "%v (fix your kno.toml, or unset KNO_CONFIG)", err)
	}
	r.label = hostLabel(e)
	reportHost(r, gatherHost(e, verify.Platform()))
	reportTools(r, CheckAll())
	reportBuildEnv(r, compile.NativeBuild())
	reportGenerate(r, e.Config)
	reportConffiles(r)
	reportIdentity(r, e)

	r.section("repository data")
	if r.remote && r.verbose {
		r.line("       the per-key lint is the same data on every host, so it is counted")
		r.line("       here and not listed; 'kno doctor package -v' lists it once.")
	}
	checkPackage(r, e)

	if r.failed() {
		r.line("\n%d check(s) failed", r.fails)
		return 1
	}
	r.line("\nall checks passed")
	return 0
}

func reportBuildEnv(r *Result, native bool) {
	r.section("build environment")
	if native {
		r.check("native build", true, "kno build compiles on this %s host itself and needs no container engine", runtime.GOOS)
		return
	}
	engine := compile.Podman()
	out, err := run(engine, "version", "--format", "{{.Client.Version}}")
	if err != nil {

		if r.remote {
			r.line("       no container engine here (%s), which is expected on a host"+
				" that receives pushes; the machine you are asking from builds.", engine)
			return
		}
		r.warn("container engine", "%s is not usable here, so builds will not run (%s)",
			engine, firstLine(out, err))
		return
	}
	r.check("container engine", true, "%s %s", engine, out)

	arch, _ := run(engine, "info", "--format", "{{.Host.Arch}}")
	rootless, _ := run(engine, "info", "--format", "{{.Host.Security.Rootless}}")

	note := ""
	if os.Geteuid() == 0 {
		note = " (as root; a build run by an operator's own account may differ)"
	}
	r.check("engine info", arch != "", "arch=%s rootless=%s%s", arch, rootless, note)

	if conn := os.Getenv("CONTAINER_CONNECTION"); conn != "" {
		r.warn("connection", "CONTAINER_CONNECTION=%s", conn)
	}

	if runtime.GOOS != "darwin" {
		r.check("platform", true, "GOARCH=%s", runtime.GOARCH)
		return
	}
	state, mErr := run(engine, "machine", "info", "--format", "{{.Host.MachineState}}")
	r.check("podman machine", mErr == nil && state != "", "state=%s", state)
	home := os.Getenv("HOME")
	wd, _ := os.Getwd()
	if home != "" && strings.HasPrefix(wd, home) {
		r.check("workdir shared", true, "%s is under $HOME (bind-mountable)", wd)
	} else {
		r.warn("workdir shared", "%s may not be shared into the podman machine; keep work dirs under $HOME", wd)
	}
	if arch != "" && arch != "amd64" {
		r.warn("platform", "machine arch %s; pin --platform for amd64 deploy targets", arch)
	}
}

func firstLine(out string, err error) string {
	if out != "" {
		line, _, _ := strings.Cut(out, "\n")
		return line
	}
	return err.Error()
}

func packageCommand() *cobra.Command {
	return &cobra.Command{
		Use:          "package",
		Short:        "check the keys in the repository data, without a container engine",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {

			r := newResult(cmd.OutOrStdout(), true)
			r.remote = false
			e, err := track.LoadEnv()
			if err != nil {
				r.check("config", false, "%v (fix your kno.toml, or unset KNO_CONFIG)", err)
			}
			checkPackage(r, e)
			if r.failed() {
				r.line("\n%d check(s) failed", r.fails)
				return &cli.ExitError{Code: 1}
			}
			r.line("\nconfig package is self-consistent")
			return nil
		},
	}
}

func toolsCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "tools",
		Short:        "list the programs that kno runs, and show which are installed",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			all := CheckAll()
			r := newResult(cmd.OutOrStdout(), false)
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(all); err != nil {
					return err
				}
				for _, s := range all {
					if s.Scope == ScopeCore && !s.Found {
						r.fails++
					}
				}
			} else {
				reportTools(r, all)
			}
			if r.failed() {
				return &cli.ExitError{Code: 1}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the table as JSON")
	return cmd
}

func run(program string, args ...string) (string, error) {
	cmd := exec.Command(program, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	out := strings.TrimSpace(string(b))
	if err != nil && out == "" {

		return strings.TrimSpace(stderr.String()), err
	}
	return out, err
}
