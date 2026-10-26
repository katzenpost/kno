package preflight

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/layout"
	"github.com/katzenpost/kno/internal/service"
	"github.com/katzenpost/kno/internal/source"
	"github.com/katzenpost/kno/internal/sshx"
	"github.com/katzenpost/kno/internal/track"
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	var network, target, host string
	cmd := &cobra.Command{
		Use:          "preflight",
		Short:        "check that a host accepts the next release and can roll back",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo := config.RepoDir()
			if repo == "" {
				return fmt.Errorf("cannot locate the network repo (set KNO_REPO)")
			}
			c, err := config.Load(config.ResolvePath(""))
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			e, err := track.LoadEnv()
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			hosts := config.ClaimedHosts(repo, c)
			if host != "" {
				hosts = filterHosts(hosts, strings.Split(host, ","))
			}
			if len(hosts) == 0 {
				return fmt.Errorf("no claimed host to check. 'kno setup' claims a node")
			}
			reports, err := Run(repo, network, target, e.StateDir, e.ReleasesDir, c, hosts)
			if err != nil {
				return err
			}
			return cli.Exit(ReportAll(cmd.OutOrStdout(), reports))
		},
	}
	f := cmd.Flags()
	f.StringVar(&network, "network", "namenlos", "network name")
	f.StringVar(&target, "target", "", "the tag about to be installed (default: the pinned ref)")
	f.StringVar(&host, "host", "", "check these hosts only, comma separated")
	return cmd
}

type Target struct {
	Label, SSH string
	Services   []string
	Client     bool
}

func Run(repo, network, target, stateRoot, releasesDir string, c config.Config, hosts []string) ([]Report, error) {
	var ts []Target
	var missing []string
	for _, label := range hosts {
		h, ok := c.Hosts[label]
		if !ok {

			missing = append(missing, label)
			continue
		}
		svcs := config.HostServices(repo, c, h)
		if len(svcs) == 0 {
			svcs = h.Services
		}
		ts = append(ts, Target{Label: label, SSH: h.SSH, Services: svcs,
			Client: c.RunsKpclientd(label)})
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("the config names no host %s, so it was not"+
			" checked", strings.Join(missing, ", "))
	}
	return CheckIn(repo, network, target, stateRoot, releasesDir, ts)
}

func CheckIn(repo, network, target, stateRoot, releasesDir string, hosts []Target) ([]Report, error) {
	if target == "" {
		target = pinnedRef(repo)
	}
	var out []Report
	for _, h := range hosts {
		local := ReadLocalState(knopath.HostState(stateRoot, h.Label), releasesDir)
		pkgs := ReleasePackages(releasesDir, target)
		in, err := probeHost(repo, network, target, h, local.Current)
		runs := ""
		if err == nil {
			if t := runningTag(installedPackages(in.State)["katzenpost"]); t != "" && t != local.Current {
				if again, err := probeHost(repo, network, target, h, t); err == nil {
					in, runs = again, t
				}
			}
		}
		in.Local, in.ReleasesDir, in.Packages = local, releasesDir, pkgs
		if err != nil {

			r := Judge(Input{Host: h.Label, Target: target, Local: local,
				ReleasesDir: releasesDir, State: map[string]string{},
				Packages: pkgs})
			r.add(Warn, "reachable", "%v", err)
			out = append(out, r)
			continue
		}
		r := Judge(in)
		if runs != "" {
			r.add(Info, "state", "kno's state says %s, and the host runs %s", cmp.Or(local.Current, "nothing"), runs)
		}
		out = append(out, r)
	}
	return out, nil
}

func runningTag(katzenpost string) string {
	base, _, _ := strings.Cut(katzenpost, "+")
	base, _, _ = strings.Cut(base, "-")
	if base == "" {
		return ""
	}
	return "v" + base
}

func probeHost(repo, network, target string, h Target, current string) (Input, error) {
	svcs := h.Services
	migrating := layout.Legacy(current) && !layout.Legacy(target)
	dirs, newDirs, unitOf := map[string]string{}, map[string]string{}, map[string]string{}
	var units []string
	reserve := 0
	for _, svc := range svcs {
		s, err := service.ResolveService(repo, network, svc)
		if err != nil {
			continue
		}
		if r := replicaReserve(repo, network, svc); r > reserve && !layout.Legacy(target) {
			reserve = r
		}
		if d := layout.StateDir(s, current); d != "" {
			dirs[svc] = d
			if migrating && d != s.StateDir && s.StateDir != "" {
				newDirs[svc] = s.StateDir
			}
		}
		if u := layout.Unit(s, current); u != "" {
			units = append(units, u)
			unitOf[svc] = u
		}
	}
	probe := Probe(dirs, units) + migrationProbe(newDirs, h.Client)
	if migrating {
		probe += courierProbe()
	}
	out, err := runRemote(h.SSH, h.Label, probe)
	if err != nil {
		return Input{}, err
	}
	return Input{
		Host: h.Label, Target: target, State: Parse(out),
		Services: svcs, Units: units, UnitOf: unitOf,
		Migrating: migrating, Dirs: dirs, NewDirs: newDirs, Client: h.Client,
		ReserveMiB: reserve,
	}, nil
}

var reserveLine = regexp.MustCompile(`^\s*MinFreeStorageMiB\s*=\s*(\d+)`)

func replicaReserve(repo, network, svc string) int {
	b, err := os.ReadFile(filepath.Join(knopath.ConfigsDir(repo, network), svc+".toml"))
	if err != nil {
		return 0
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if m := reserveLine.FindStringSubmatch(ln); m != nil {
			n, _ := strconv.Atoi(m[1])
			return n
		}
	}
	return 0
}

func runRemote(node, label, probe string) (string, error) {
	if node == "" {
		b, err := exec.Command("sh", "-c", probe).CombinedOutput()
		return string(b), err
	}
	argv := sshx.SSH(sshx.Program(), node, probe)
	argv = append(argv[:1:1], append([]string{"-o", "ConnectTimeout=10"}, argv[1:]...)...)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
	if ctx.Err() != nil {
		return "", fmt.Errorf("%s did not answer within 60s", node)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 255 {
		return "", fmt.Errorf("ssh cannot connect to %s: %s", node, strings.TrimSpace(string(out)))
	}
	if err != nil && len(out) == 0 {
		return "", fmt.Errorf("ssh %s: %v", node, err)
	}
	return string(out), nil
}

func pinnedRef(repo string) string {
	pins, _ := source.ParsePins(filepath.Join(repo, "thirdparty", "pins.env"))
	return pins["KATZENPOST_REF"]
}

func filterHosts(all, want []string) []string {
	var out []string
	for _, w := range want {
		w = strings.TrimSpace(w)
		for _, a := range all {
			if a == w {
				out = append(out, a)
			}
		}
	}
	return out
}

func ReportAll(w io.Writer, reports []Report) int {
	if len(reports) == 0 {
		fmt.Fprintf(w, "nothing was checked, so nothing is known.\n"+
			"  No host produced a report. That is not a pass: run 'kno config'\n"+
			"  to see which hosts this organizer has.\n")
		return 1
	}
	blocked := 0
	for _, r := range reports {
		fmt.Fprintf(w, "==> %s\n", r.Host)
		tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
		for _, f := range r.Findings {
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", f.Severity, f.Check, f.Detail)
		}
		tw.Flush()
		if r.Blocked() {
			blocked++
		}
	}
	if blocked > 0 {
		fmt.Fprintf(w, "\n%d host(s) must not be upgraded yet.\n", blocked)
		return 1
	}
	fmt.Fprintf(w, "\n%d host(s) ready.\n", len(reports))
	return 0
}
