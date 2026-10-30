package build

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/knover"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/pkg/freebsd"
	"github.com/katzenpost/kno/internal/service"
	"github.com/katzenpost/kno/internal/source"
)

type flags struct {
	tag, svcList, arches                    string
	host, group, node                       string
	export, network, revision, toolRevision string
	allHosts, allNodes                      bool
	warped, binOnly, offline                bool
	skipFbsd, skipDocker                    bool
	skipTarball                             bool
	native, dry, verbose                    bool
	qemu                                    bool
	imagesOnly                              bool
	force                                   bool
}

func Command() *cobra.Command {
	var v flags
	cmd := &cobra.Command{
		Use:          "build",
		Short:        "build binary and config packages",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			v.dry, v.verbose = cli.DryRun(), cli.Verbose()
			runBuild(cmd, v)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&v.tag, "tag", "", "katzenpost tag to build (default: KATZENPOST_REF in pins.env)")
	f.StringVar(&v.host, "host", "", "scope the build to these hosts (comma separated labels from kno.toml)")
	f.StringVar(&v.group, "group", "", "scope the build to an operator's node group, comma separated for several")
	f.BoolVar(&v.allHosts, "all-hosts", false, "scope the build to every host in the config")
	f.StringVar(&v.node, "node", "", "scope the build to these nodes of the"+
		" network, comma separated. It needs no host in kno.toml")
	f.BoolVar(&v.allNodes, "all-nodes", false, "build every node of the network")
	f.StringVar(&v.svcList, "services", "", "explicit service list, comma separated. A build jail has no host config to scope from")
	f.StringVar(&v.arches, "arches", "", "arch list, space or comma separated (default: the arches of the selected services' nodes)")
	f.StringVar(&v.export, "export", "", "also copy the finished packages into this directory")
	f.StringVar(&v.network, "network", "namenlos", "network name")
	f.StringVar(&v.revision, "revision", "", "katzenpost revision: a number for a patched katzenpost package of the same tag (1, 2, ...)")
	f.StringVar(&v.toolRevision, "tool-revision", "",
		"kno revision: a number for new kno, kno-host-config and config packages of the same tag")
	f.BoolVar(&v.warped, "warped", false, "epoch-warped test build")
	f.BoolVar(&v.binOnly, "binary-only", false, "build just the katzenpost binary package")
	f.BoolVar(&v.offline, "offline", false, "offline build (mount GOMODCACHE)")
	f.BoolVarP(&v.force, "force", "f", false, "rebuild even when this release is already built from these inputs")
	f.BoolVar(&v.skipFbsd, "skip-freebsd", false, "do not build the FreeBSD package")
	f.BoolVar(&v.skipDocker, "skip-docker", false, "do not build the container images")
	f.BoolVar(&v.skipTarball, "skip-tarball", false, "do not build the binary and source tarballs")
	f.BoolVar(&v.native, "native", compile.NativeBuild(),
		"build with the host Go toolchain and emit the host package format, no container. Default on freebsd")
	f.BoolVar(&v.qemu, "qemu", false, "build each katzenpost package in a"+
		" container of its own arch under qemu, not with a cross compiler")
	f.BoolVar(&v.imagesOnly, "images", false,
		"prepare the build images for the selected hosts and stop")

	cmd.AddCommand(source.PinCommand(), freebsd.SysrootCommand(), freebsd.VMImageCommand(),
		compareCommand(), recordCommand(), warpedCommand(), versionCommand(), epochCommand())
	return cmd
}

func versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:          "version",
		Short:        "print the version of the kno package this tree builds",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo := config.RepoDir()
			pins, err := source.ParsePins(filepath.Join(repo, "thirdparty", "pins.env"))
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), knover.Version(repo, pins["KATZENPOST_REF"]))
			return nil
		},
	}
}

func epochCommand() *cobra.Command {
	var network string
	cmd := &cobra.Command{
		Use:          "epoch",
		Short:        "print the SOURCE_DATE_EPOCH that kno build uses for this tree",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo := config.RepoDir()
			pins, err := source.ParsePins(filepath.Join(repo, "thirdparty", "pins.env"))
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(),
				epochFor(Options{Repo: repo, Network: network, Tag: pins["KATZENPOST_REF"]}))
			return nil
		},
	}
	cmd.Flags().StringVar(&network, "network", "namenlos", "network name")
	return cmd
}

func recordCommand() *cobra.Command {
	var network, tag, dir string
	var prune bool
	cmd := &cobra.Command{
		Use:          "record",
		Short:        "record the digests of this build in the network repository",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo := config.RepoDir()
			var err error
			if tag, err = recordTag(repo, tag); err != nil {
				return err
			}
			if dir == "" {
				dir = filepath.Join(knopath.Artifacts(), "releases", tag, "pkgs")
			}
			rec := RecordCommitted
			if prune {
				rec = RecordPruning
			}
			wrote, err := rec(repo, network, tag, dir)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			reportStrangers(w, repo, network, tag)
			if len(wrote) == 0 {
				fmt.Fprintf(w, "%s is already recorded and this build matches it\n", tag)
				return nil
			}
			for _, p := range wrote {
				fmt.Fprintf(w, "wrote %s\n", p)
			}
			fmt.Fprintf(w, "\nCommit the files. Later builds then compare their packages with this record:\n")
			fmt.Fprintf(w, "  git -C %s add %s\n", repo, CommittedDir(repo, network, tag))
			fmt.Fprintf(w, "  git -C %s commit -m 'Record %s'\n", repo, tag)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&network, "network", "namenlos", "network name")
	f.StringVar(&tag, "tag", "", "katzenpost tag to record (default: KATZENPOST_REF in data/thirdparty/pins.env)")
	f.StringVar(&dir, "dir", "",
		"the directory holding this build's "+ManifestName+
			" (default: this tag's release directory)")
	f.BoolVar(&prune, "prune", false,
		"drop recorded packages that this build does not make."+
			" Use it only on the host that builds the whole release")
	return cmd
}

func reportStrangers(w io.Writer, repo, network, tag string) {
	c, err := config.Load(config.ResolvePath(""))
	if err != nil {
		return
	}
	svcs := config.ClaimedServices(repo, network, c.Nodes)
	strangers := UnclaimedInRecord(Recorded(CommittedDir(repo, network, tag)), svcs)
	if len(strangers) == 0 {
		return
	}
	fmt.Fprintf(w, "kno build record: The record of %s has config packages for %d"+
		" services that are not in your nodes list:\n", tag, len(strangers))
	for _, s := range strangers {
		fmt.Fprintf(w, "  %s\n", s)
	}
	fmt.Fprint(w, "  If another operator built them, this is correct. If a node left your\n"+
		"  list, nobody builds its package again. Then the record names a\n"+
		"  package that no host can make. To remove those entries, run:\n"+
		"    kno build record --prune\n")
}

func (o Options) formats() string {
	var out []string
	if !o.SkipDeb {
		out = append(out, "deb")
	}
	if !o.SkipFreeBSD {
		out = append(out, "freebsd")
	}
	return strings.Join(out, "+")
}

func BackendFormats(repo, network string, svcs []string) map[string]bool {
	out := map[string]bool{}
	for _, svc := range svcs {
		if f := backendFormat(repo, network, svc); f != "" {
			out[f] = true
		}
	}
	return out
}

func backendFormat(repo, network, svc string) string {
	rs, err := service.ResolveService(repo, network, svc)
	if err != nil {
		return ""
	}
	switch rs.Backend {
	case "freebsd-ezjail":
		return "freebsd"
	case "docker":
		return "docker"
	}
	return "deb"
}

func debService(repo, network, svc string) bool {
	return backendFormat(repo, network, svc) == "deb"
}

func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })
}

func groupNodes(repo, network string, groups []string) (map[string]bool, error) {
	ops := config.LoadOperators(repo, network)
	want := map[string]bool{}
	for _, g := range groups {
		nodes, ok := ops[g]
		if !ok {
			names := make([]string, 0, len(ops))
			for n := range ops {
				names = append(names, n)
			}
			sort.Strings(names)
			return nil, fmt.Errorf("no operator group %q (known: %s)", g, strings.Join(names, " "))
		}
		for _, n := range nodes {
			want[n] = true
		}
	}
	return want, nil
}

func groupHosts(repo, network string, c config.Config, groups []string) ([]string, error) {
	want, err := groupNodes(repo, network, groups)
	if err != nil {
		return nil, err
	}
	var out []string
	for label, h := range c.Hosts {
		for _, s := range config.HostServices(repo, c, h) {
			if n, _ := config.ServiceNode(repo, network, s); want[n] {
				out = append(out, label)
				break
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

func groupServices(repo, network string, groups []string) ([]string, error) {
	want, err := groupNodes(repo, network, groups)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, svc := range gcServices(repo, network) {
		if n, _ := config.ServiceNode(repo, network, svc); want[n] {
			out = append(out, svc)
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("operator group(s) %s have no services in %s",
			strings.Join(groups, " "), network)
	}
	return out, nil
}

func selectHosts(repo string, v flags, c config.Config) ([]string, error) {

	host := v.host
	if !scoped(v) {
		return nil, nil
	}

	var chosen []string
	switch {
	case v.allHosts:
		for label := range c.Hosts {
			chosen = append(chosen, label)
		}
		sort.Strings(chosen)
	case v.group != "":
		g, err := groupHosts(repo, v.network, c, splitList(v.group))
		if err != nil {
			return nil, err
		}
		chosen = g
	case host != "":
		chosen = splitList(host)
	}
	for _, h := range chosen {
		if _, ok := c.Hosts[h]; !ok {
			known := make([]string, 0, len(c.Hosts))
			for k := range c.Hosts {
				known = append(known, k)
			}
			sort.Strings(known)
			return nil, fmt.Errorf("host %q not in config (known: %s)", h, strings.Join(known, " "))
		}
	}
	if len(chosen) == 0 {
		return nil, fmt.Errorf("that selection matched no host in the config")
	}
	return chosen, nil
}

func recordTag(repo, tag string) (string, error) {
	if repo == "" {
		return "", fmt.Errorf("cannot locate the network repo (set KNO_REPO)")
	}
	if tag == "" {
		pins, err := source.ParsePins(filepath.Join(repo, "thirdparty", "pins.env"))
		if err != nil {
			return "", fmt.Errorf("reading pins: %w", err)
		}
		tag = pins["KATZENPOST_REF"]
	}
	if tag == "" {
		return "", fmt.Errorf("no tag (pass --tag or set" +
			" KATZENPOST_REF in pins.env)")
	}
	tag, _ = deb.SplitRevision(tag)
	return tag, nil
}

func oneScope(v flags) error {
	var given []string
	for name, set := range map[string]bool{
		"--services": v.svcList != "", "--all-nodes": v.allNodes,
		"--node": v.node != "", "--all-hosts": v.allHosts,
		"--group": v.group != "", "--host": v.host != "",
	} {
		if set {
			given = append(given, name)
		}
	}
	if len(given) > 1 {
		sort.Strings(given)
		return fmt.Errorf("%s are alternatives. Pass one",
			strings.Join(given, " and "))
	}
	return nil
}

func nodeServices(repo, network string, nodes []string) ([]string, error) {
	known := config.ListNodes(repo, network)
	for _, n := range nodes {
		if !slices.Contains(known, n) {
			return nil, fmt.Errorf("no node %q in %s (known: %s)", n, network,
				strings.Join(known, " "))
		}
	}
	return config.ClaimedServices(repo, network, nodes), nil
}

func scoped(v flags) bool {
	return v.host != "" || v.group != "" || v.allHosts
}

func configScope(repo string, c config.Config) ([]string, string) {
	if label, h, ok := config.LocalHost(c); ok && len(config.HostServices(repo, c, h)) > 0 {
		return []string{label}, "this host is " + label + "; building its services"
	}

	hosts := config.ClaimedHosts(repo, c)
	if len(hosts) == 0 {
		return nil, ""
	}
	note := fmt.Sprintf("This host is the organizer. kno builds for its %d hosts: %s.",
		len(hosts), strings.Join(hosts, " "))
	if skipped := config.UnclaimedHosts(repo, c); len(skipped) > 0 {
		note += fmt.Sprintf("\nkno build: kno does not build for %s. kno.toml lists it in"+
			" [hosts] but not in nodes. To add the node, run kno setup.", strings.Join(skipped, " "))
	}
	return hosts, note
}

func localPeers(c config.Config, hosts []string) map[string]string {
	if label, _, ok := config.LocalHost(c); ok && len(hosts) == 1 && hosts[0] == label {
		return c.Peers
	}
	return nil
}

func hostServices(repo string, c config.Config, hosts []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, label := range hosts {
		for _, s := range config.HostServices(repo, c, c.Hosts[label]) {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	sort.Strings(out)
	return out
}

func runBuild(cmd *cobra.Command, v flags) {
	if v.verbose {
		executable, _ := os.Executable()
		fmt.Fprintf(cmd.ErrOrStderr(), "kno build: running kno %s, %s, %s/%s, executable %s\n",
			cli.Version(), runtime.Version(), runtime.GOOS, runtime.GOARCH, executable)
	}
	repo := config.RepoDir()
	if repo == "" {
		fmt.Fprintln(os.Stderr, "kno build: cannot locate the network repo (set KNO_REPO)")
		os.Exit(1)
	}
	pins, err := source.ParsePins(filepath.Join(repo, "thirdparty", "pins.env"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "kno build: reading pins: %v\n", err)
		os.Exit(1)
	}
	t := v.tag
	if t == "" {
		t = pins["KATZENPOST_REF"]
	}
	if t == "" {
		fmt.Fprintln(os.Stderr, "kno build: no tag (pass --tag or set KATZENPOST_REF in pins.env)")
		os.Exit(1)
	}

	t, rev := deb.SplitRevision(t)
	if v.revision != "" {
		rev = v.revision
	}

	toolRev := v.toolRevision
	if rev == "0" {
		fmt.Fprintln(os.Stderr, "kno build: WARN: dpkg sees revision -0 as no revision."+
			" These packages do not replace an installed package without a revision."+
			" Use revision 1 or higher.")
	}

	o := Options{
		Repo: repo, Network: v.network, Tag: t,

		Dist:   knopath.Dist(),
		Warped: v.warped, Offline: v.offline, BinaryOnly: v.binOnly, Verbose: v.verbose, Force: v.force,
		Revision:     rev,
		ToolRevision: toolRev,
		Native:       v.native,
		Qemu:         v.qemu,

		SkipFreeBSD: v.skipFbsd || os.Getenv("KNO_SKIP_FREEBSD") == "1" || v.binOnly,
		SkipDocker:  v.skipDocker || os.Getenv("KNO_SKIP_DOCKER") == "1" || v.binOnly,
		SkipTarball: v.skipTarball || v.binOnly,
	}

	if err := oneScope(v); err != nil {
		fmt.Fprintf(os.Stderr, "kno build: %v\n", err)
		os.Exit(1)
	}
	fromConfig := false
	scopeNote := ""
	switch {
	case v.allNodes:
		o.Services = gcServices(repo, v.network)
		scopeNote = "all nodes"
	case v.node != "":
		svcs, err := nodeServices(repo, v.network, splitList(v.node))
		if err != nil {
			fmt.Fprintf(os.Stderr, "kno build: %v\n", err)
			os.Exit(1)
		}
		o.Services = svcs
		scopeNote = "node " + v.node
	case v.svcList != "":
		o.Services = splitList(v.svcList)
		if len(o.Services) == 0 {
			fmt.Fprintln(os.Stderr, "kno build: --services was empty")
			os.Exit(1)
		}
	case v.binOnly:

	case scoped(v):
		c, err := config.Load(config.ResolvePath(""))
		if err != nil {
			fmt.Fprintf(os.Stderr, "kno build: loading config: %v\n", err)
			os.Exit(1)
		}
		hosts, err := selectHosts(repo, v, c)
		if err != nil {

			if v.group != "" {
				svcs, gerr := groupServices(repo, v.network, splitList(v.group))
				if gerr != nil {
					fmt.Fprintf(os.Stderr, "kno build: %v\n", gerr)
					os.Exit(1)
				}
				fmt.Fprintf(os.Stderr, "kno build: no host in your config runs %s."+
					" kno builds its %d service(s) from the network data"+
					" (no per-host config package)\n", v.group, len(svcs))
				o.Services = svcs
				scopeNote = "group " + v.group + ", not hosted here"
				break
			}
			fmt.Fprintf(os.Stderr, "kno build: %v\n", err)
			os.Exit(1)
		}
		o.Services = hostServices(repo, c, hosts)
		o.Peers = localPeers(c, hosts)

		o.Hosts, o.Config = hosts, &c
		if len(o.Services) == 0 {
			fmt.Fprintf(os.Stderr, "kno build: %s has no placed services\n", strings.Join(hosts, " "))
			os.Exit(1)
		}
	default:

		if c, err := config.Load(config.ResolvePath("")); err == nil {
			if config.ClaimsNoNode(c) {
				fmt.Fprintf(os.Stderr, "kno build: %s\n", config.NoNodeHint)
				os.Exit(1)
			}
			if hosts, why := configScope(repo, c); len(hosts) > 0 {
				if v.verbose || strings.Contains(why, "not building") {
					fmt.Fprintf(os.Stderr, "kno build: %s\n", why)
				}
				o.Services = hostServices(repo, c, hosts)
				o.Peers = localPeers(c, hosts)
				o.Hosts, o.Config = hosts, &c
				fromConfig = true
			}
		}
		if len(o.Services) == 0 {
			o.Services = gcServices(repo, v.network)
		}
	}

	narrowed := scoped(v) || fromConfig || v.svcList != "" || v.node != ""
	if narrowed && len(o.Services) > 0 && !v.native {
		formats := BackendFormats(repo, v.network, o.Services)
		if !cmd.Flags().Changed("skip-freebsd") {
			o.SkipFreeBSD = !formats["freebsd"]
		}
		if !cmd.Flags().Changed("skip-docker") {
			o.SkipDocker = !formats["docker"]
		}
		o.SkipDeb = !formats["deb"]
	}

	o.Arches = buildArches(repo, v, o.Services)

	verbose = o.Verbose
	switch {
	case !o.Verbose && !v.dry:
	case o.Native:
		fmt.Fprintf(os.Stderr, "kno build: network %s, tag %s, native %s, %d services\n",
			v.network, t, runtime.GOARCH, len(o.Services))
	case o.BinaryOnly:
		fmt.Fprintf(os.Stderr, "kno build: binaries only, tag %s, architectures %s\n",
			t, strings.Join(o.Arches, " "))
	default:
		fmt.Fprintf(os.Stderr, "kno build: network %s, tag %s, architectures %s, %d services %s, formats %s\n",
			v.network, t, strings.Join(o.Arches, " "), len(o.Services), hostScopeFor(o.Hosts, scopeNote), o.formats())
	}

	if v.imagesOnly {
		if err := o.PrepareImages(os.Stderr); err != nil {
			fmt.Fprintf(os.Stderr, "kno build: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if v.dry {
		fmt.Fprintf(os.Stderr, "kno build: packages -> %s\n", filepath.Join(o.Dist, "releases", o.Tag, "pkgs"))
		for _, s := range o.Services {
			fmt.Println(s)
		}
		return
	}

	pkgs, err := Build(o)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kno build: %v\n", err)
		os.Exit(1)
	}
	if err := listBuilt(os.Stdout, pkgs); err != nil {
		fmt.Fprintf(os.Stderr, "kno build: %v\n", err)
		os.Exit(1)
	}

	if v.export != "" {
		n, err := export(pkgs, v.export)
		if err != nil {
			fmt.Fprintf(os.Stderr, "kno build: export: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "kno build: copied %d package(s) to %s\n", n, v.export)
	}
}

func listBuilt(w io.Writer, pkgs []string) error {
	pkgs = slices.Sorted(slices.Values(pkgs))
	for _, p := range pkgs {
		sum, _, err := fsutil.SHA256File(p)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "%s  %s\n", sum, p)
	}
	return nil
}

func buildArches(repo string, v flags, svcs []string) []string {
	if v.arches == "" && !v.native && len(svcs) > 0 {
		if a := ArchesForServices(repo, v.network, svcs); len(a) > 0 {
			return a
		}
	}
	return splitArches(v.arches)
}

func hostScopeFor(hosts []string, why string) string {
	if len(hosts) == 0 && why != "" {
		return "(" + why + ")"
	}
	switch len(hosts) {
	case 0:
		return "(whole network, no host scope)"
	case 1:
		return "on " + hosts[0]
	default:
		return fmt.Sprintf("on %d hosts (%s)", len(hosts), strings.Join(hosts, " "))
	}
}

func export(pkgs []string, dir string) (int, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	for i, p := range pkgs {
		if err := fsutil.CopyFile(p, filepath.Join(dir, filepath.Base(p))); err != nil {
			return i, err
		}
	}
	seen := map[string]bool{}
	for _, p := range pkgs {
		src := filepath.Dir(p)
		if seen[src] {
			continue
		}
		seen[src] = true
		for _, name := range []string{SumsName, ManifestName} {
			from := filepath.Join(src, name)
			if _, err := os.Stat(from); err != nil {
				continue
			}
			if err := fsutil.CopyFile(from, filepath.Join(dir, name)); err != nil {
				return len(pkgs), fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	return len(pkgs), nil
}

func compareCommand() *cobra.Command {
	var dir, network string
	cmd := &cobra.Command{
		Use: "compare [manifest.json | first-build-dir]",
		Short: "check a build against the record in the repo," +
			" a release manifest, or a first build",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				repo := config.RepoDir()
				tag, err := recordTag(repo, "")
				if err != nil {
					return err
				}
				if dir == "" {
					dir = filepath.Join(knopath.Artifacts(), "releases", tag, "pkgs")
				}
				w := cmd.OutOrStdout()
				return cli.Exit(compareRecord(w, repo, network, tag, dir))
			}
			ref, err := LoadBuild(args[0])
			if err != nil {
				return err
			}
			if dir == "" {
				dir = filepath.Join(knopath.Artifacts(), "releases", ref.Tag, "pkgs")
			}
			var got PackageSums
			if ref.Dir != "" {
				got, err = DirSums(dir)
			} else {
				got, err = LoadManifest(filepath.Join(dir, ManifestName))
			}
			if err != nil {
				return fmt.Errorf("%w\n  --dir is the package directory of this build", err)
			}
			absent := Uncovered(got)
			if ref.Dir == "" && !namesSelfPackage(ref) {
				ref, got = withoutSelfPackages(ref), withoutSelfPackages(got)
			}
			diffs := append(Compare(ref, got), absent...)
			return cli.Exit(ReportCompare(cmd.OutOrStdout(), ref, got, diffs))
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "",
		"the package directory of this build (default: this tag's release directory)")
	cmd.Flags().StringVar(&network, "network", "namenlos", "network name")
	return cmd
}

func warpedCommand() *cobra.Command {
	var v flags
	cmd := &cobra.Command{
		Use:          "warped",
		Short:        "build the packages with epoch-warped binaries",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			v.dry, v.verbose = cli.DryRun(), cli.Verbose()
			v.warped = true

			v.binOnly = false
			runBuild(cmd, v)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&v.tag, "tag", "", "katzenpost tag to build (default: KATZENPOST_REF in data/thirdparty/pins.env)")
	f.StringVar(&v.host, "host", "", "scope to these hosts (comma separated labels from kno.toml)")
	f.StringVar(&v.group, "group", "", "scope to an operator's node group")
	f.BoolVar(&v.allHosts, "all-hosts", false, "scope to every host in the config")
	f.StringVar(&v.svcList, "services", "", "explicit service list, comma separated")
	f.StringVar(&v.arches, "arches", "", "arch list (default: the arches of the selected services' nodes)")
	f.StringVar(&v.export, "export", "", "also copy the finished packages into this directory")
	f.StringVar(&v.network, "network", "namenlos", "network name")
	f.StringVar(&v.revision, "revision", "", "Debian package revision")
	f.BoolVarP(&v.force, "force", "f", false, "rebuild even when this release is already built from these inputs")

	v.skipFbsd, v.skipDocker = true, true
	return cmd
}
