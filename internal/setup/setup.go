package setup

import (
	"bufio"
	"cmp"
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/schedule"
)

var systemConfigPath = config.SystemConfigPath()

var quit = os.Exit

func ask(r *bufio.Reader, q, def string) string {
	if def != "" {
		fmt.Fprintf(os.Stderr, "%s [%s]: ", q, def)
	} else {
		fmt.Fprintf(os.Stderr, "%s: ", q)
	}
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(os.Stderr, "\nkno setup: the input ended; nothing written")
		quit(1)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func yes(s string) bool { return strings.HasPrefix(strings.ToLower(s), "y") }

func overlay(base, over config.Config) config.Config {
	out := base
	if over.Network != "" {
		out.Network = over.Network
	}
	if len(over.Nodes) > 0 {
		out.Nodes = over.Nodes
	}
	if over.Update.Policy != "" {
		out.Update.Policy = over.Update.Policy
	}
	if over.Update.RequireSignedTag {
		out.Update.RequireSignedTag = true
	}
	if over.Update.Threshold > 0 {
		out.Update.Threshold = over.Update.Threshold
	}
	if over.Push.Mode != "" {
		out.Push.Mode = over.Push.Mode
	}
	d, od := &out.Defaults, over.Defaults
	d.Privilege = cmp.Or(od.Privilege, d.Privilege)
	d.Scheduler = cmp.Or(od.Scheduler, d.Scheduler)
	d.KpclientdUser = cmp.Or(od.KpclientdUser, d.KpclientdUser)
	d.Install = cmp.Or(od.Install, d.Install)
	if od.Kpclientd != nil {
		d.Kpclientd = od.Kpclientd
	}
	if len(over.Peers) > 0 {
		peers := maps.Clone(out.Peers)
		if peers == nil {
			peers = map[string]string{}
		}
		maps.Copy(peers, over.Peers)
		out.Peers = peers
	}
	if len(over.Hosts) > 0 {
		merged := map[string]config.Host{}
		for l, h := range out.Hosts {
			merged[l] = h
		}
		for l, h := range over.Hosts {
			merged[l] = h
		}
		out.Hosts = merged
	}
	return out
}

func privilege(r *bufio.Reader, label, prior string) string {
	q := "  privileges on " + label + " (none/sudo/kno/apt)"
	if label == "" {
		q = "privileges (none/sudo/kno/apt)"
	}
	for {
		v := strings.TrimSpace(ask(r, q, cmp.Or(prior, config.PrivNone)))
		if config.ValidPrivilege(v) {
			return v
		}
		fmt.Fprintf(os.Stderr, "    not one of %s\n", strings.Join(config.Privileges, ", "))
	}
}

func privilegeLegend(w io.Writer) {
	fmt.Fprintln(w, "\nWho runs the steps that need root?")
	for _, l := range config.PrivilegeHelp {
		fmt.Fprintf(w, "  %s\n", l)
	}
}

func groupDefaults(r *bufio.Reader, prior config.HostDefaults) config.HostDefaults {
	fmt.Fprintln(os.Stderr, "\n== defaults for every host (override individually next) ==")
	d := config.HostDefaults{}
	d.Scheduler = ask(r, "keep hosts updated with (timer/cron/none)", cmp.Or(prior.Scheduler, "none"))
	client := yes(ask(r, "run the katzenpost client daemon (kpclientd)?",
		boolDef(prior.Kpclientd == nil || *prior.Kpclientd)))
	d.Kpclientd = config.Bool(client)
	if client {
		d.KpclientdUser = clientUser(r, prior.KpclientdUser)
	}
	privilegeLegend(os.Stderr)
	d.Privilege = privilege(r, "", cmp.Or(prior.Privilege, config.PrivNone))
	return d
}

func exceptions(r *bufio.Reader, labels []string, d config.HostDefaults,
	prior map[string]config.Host) map[string]config.Host {
	out := map[string]config.Host{}
	fmt.Fprintf(os.Stderr, "\nDefaults: scheduler=%s kpclientd=%v privilege=%s\n",
		d.Scheduler, d.Kpclientd != nil && *d.Kpclientd, d.Privilege)

	var already []string
	for _, l := range labels {
		if differs(prior[l], d) {
			already = append(already, l)
		}
	}
	answer := ask(r, "any hosts that differ? (space separated; blank for none)",
		strings.Join(already, " "))
	for _, l := range strings.Fields(answer) {
		if !slices.Contains(labels, l) {
			fmt.Fprintf(os.Stderr, "  no such host: %s\n", l)
			continue
		}
		h := prior[l]
		fmt.Fprintf(os.Stderr, "  -- %s --\n", l)
		h.Scheduler = ask(r, "  keep "+l+" updated with (timer/cron/none)",
			cmp.Or(prior[l].Scheduler, d.Scheduler))
		client := yes(ask(r, "  run kpclientd on "+l+"?",
			boolDef(clientDefaultFor(prior[l], d))))
		h.Kpclientd = config.Bool(client)
		h.KpclientdUser = ""
		if client {
			h.KpclientdUser = clientUser(r, cmp.Or(prior[l].KpclientdUser, d.KpclientdUser))
		}
		h.Privilege = privilege(r, l, cmp.Or(prior[l].Privilege, d.Privilege))
		out[l] = h
	}
	return out
}

func differs(h config.Host, d config.HostDefaults) bool {
	if h.Scheduler != "" && h.Scheduler != d.Scheduler {
		return true
	}
	if h.Privilege != "" && h.Privilege != d.Privilege {
		return true
	}
	if h.KpclientdUser != "" && h.KpclientdUser != d.KpclientdUser {
		return true
	}
	if h.Kpclientd != nil && *h.Kpclientd != (d.Kpclientd != nil && *d.Kpclientd) {
		return true
	}
	return false
}

func clientDefaultFor(h config.Host, d config.HostDefaults) bool {
	if h.Kpclientd != nil {
		return *h.Kpclientd
	}
	return d.Kpclientd != nil && *d.Kpclientd
}

func clientUser(r *bufio.Reader, prior string) string {
	return strings.TrimSpace(ask(r, "  run kpclientd as which account? (blank for a dynamic user)", prior))
}

func clientDefault(prior map[string]config.Host, label string) string {

	if h, ok := prior[label]; ok && h.Kpclientd != nil && !*h.Kpclientd {
		return "n"
	}
	return "y"
}

func addUniq(xs []string, x string) []string {
	if slices.Contains(xs, x) {
		return xs
	}
	return append(xs, x)
}

func pickNode(r *bufio.Reader, all []string) string {
	return pickNodeDef(r, all, "node (number or name)", "")
}

func pickNodeDef(r *bufio.Reader, all []string, prompt, def string) string {
	for {
		sel := ask(r, prompt, def)
		for i, n := range all {
			if sel == n || sel == fmt.Sprintf("%d", i+1) {
				return n
			}
		}
		fmt.Fprintln(os.Stderr, "  not a listed node; try again")
	}
}

func nodeOf(svc string) string {
	if i := strings.Index(svc, "-pq-"); i > 0 {
		return svc[:i]
	}
	return svc
}

func matchNode(all []string, host string) string {
	if host == "" {
		return ""
	}
	for _, n := range all {
		if strings.EqualFold(n, host) {
			return n
		}
	}
	short := host
	if i := strings.IndexByte(short, '.'); i > 0 {
		short = short[:i]
	}
	for _, n := range all {
		if strings.EqualFold(n, short) {
			return n
		}
	}
	ls := strings.ToLower(short)
	for _, n := range all {
		ln := strings.ToLower(n)
		if strings.HasPrefix(ls, ln) || strings.HasPrefix(ln, ls) {
			return n
		}
	}
	return ""
}

func askHost(r *bufio.Reader, label string, prior config.Host) (string, string) {
	d := prior.SSH
	if d == "" {
		d = "this host"
	}
	where := ask(r, "  "+label+" runs on (this host, or an ssh target like user@host)", d)
	if where == "this host" || where == "local" || where == "" {
		hn, _ := os.Hostname()
		return "", hn
	}
	return where, ""
}

func isOperatorConfig(nodes []string, hosts map[string]config.Host) bool {
	if len(nodes) > 1 {
		return true
	}
	for _, h := range hosts {
		if h.SSH != "" {
			return true
		}
	}
	return false
}

func matchOperator(ops map[string][]string, names, claimed []string) string {
	if len(claimed) == 0 {
		return ""
	}
	want := map[string]bool{}
	for _, n := range claimed {
		want[n] = true
	}
	best, bestHits := "", 0
	for _, name := range names {
		hits := 0
		for _, nd := range ops[name] {
			if want[nd] {
				hits++
			}
		}
		if hits == len(claimed) && hits == len(ops[name]) {
			return name
		}
		if hits > bestHits {
			best, bestHits = name, hits
		}
	}
	return best
}

func rosterEntry(n int, name, mark string, nodes, services []string) []string {
	what := plural(len(services), "service")
	items := services
	if len(items) == 0 {
		what, items = "services unknown", nodes
	}
	head := fmt.Sprintf("  %2d) %s -- %s, %s%s", n, name, plural(len(nodes), "node"), what, mark)
	return append([]string{head}, wrapped(items, "      ", 78)...)
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

func wrapped(items []string, indent string, width int) []string {
	var out []string
	line := ""
	for _, it := range items {
		switch {
		case line == "":
			line = indent + it
		case len(line)+1+len(it) <= width:
			line += " " + it
		default:
			out = append(out, line)
			line = indent + it
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

func priorLabel(hosts map[string]config.Host, svc string) string {
	labels := make([]string, 0, len(hosts))
	for l := range hosts {
		labels = append(labels, l)
	}
	sort.Strings(labels)
	for _, l := range labels {
		for _, s := range hosts[l].Services {
			if s == svc {
				return l
			}
		}
	}
	return ""
}

func knoSchedule(sched, ssh string) error {
	args := []string{"schedule", sched}
	if ssh != "" {
		args = append(args, ssh)
	}
	cmd := exec.Command(knopath.Self(), args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func sudoInstallConfig(src string) error {
	sudo := strings.Fields(cli.Sudo())
	if len(sudo) == 0 {
		return installConfig(src, systemConfigPath)
	}
	for _, argv := range [][]string{
		{"mkdir", "-p", filepath.Dir(systemConfigPath)},
		{"install", "-m", "0644", src, systemConfigPath},
	} {
		st := schedule.Step{Argv: append(append([]string{}, sudo...), argv...)}
		if err := schedule.RunStep(context.Background(), st, os.Stderr, false); err != nil {
			return err
		}
	}
	return nil
}

func sudoPrefix() string {
	if s := cli.Sudo(); s != "" {
		return s + " "
	}
	return ""
}

func defaultDest() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".local", "katzenpost", "kno.toml"), nil
}

func thisHost(prior config.Host, hostname, sched string, client bool, user string) config.Host {
	h := prior
	h.Hostname, h.Scheduler = hostname, sched
	h.Kpclientd, h.KpclientdUser = config.Bool(client), user
	return h
}

func placedHost(prior config.Host, odd map[string]config.Host, label, ssh, hostname string,
	services []string) config.Host {
	h, ok := odd[label]
	if !ok {
		h = prior
		h.Scheduler, h.Kpclientd, h.KpclientdUser, h.Privilege = "", nil, "", ""
	}
	if ssh == "" || prior.SSH == "" {
		h.Hostname = hostname
	}
	h.SSH = ssh
	h.Services = services
	return h
}

const sharedRepoNote = `  shared repo (networks/): unchanged. To change a node or the topology, run
  'kno topology-propose'. The change reaches everyone through the committed
  version. The claim stays on this machine.`

func installConfig(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		return err
	}
	return os.Chmod(dst, 0o644)
}

func run(out string) {
	if !cli.IsTerminal(os.Stdin) || !cli.IsTerminal(os.Stderr) {
		fmt.Fprintln(os.Stderr, "kno setup needs a terminal; install the kno-host-config package for an unattended setup")
		os.Exit(1)
	}

	dest := out
	if dest == "" {
		d, err := defaultDest()
		if err != nil {
			fmt.Fprintf(os.Stderr, "kno setup: %v\n", err)
			os.Exit(1)
		}
		dest = d
	}

	repo := config.RepoDir()
	c := config.Config{}

	sysPath := config.SystemPath()
	userPath := config.ResolvePath("")
	if sysPath != "" && sysPath != userPath {
		if sc, err := config.Load(sysPath); err == nil {
			c = sc
		}
	}
	if userPath != "" {
		if ex, err := config.Load(userPath); err == nil {
			c = overlay(c, ex)
		}
	}
	if masked := config.Masked(userPath, sysPath); len(masked) > 0 {
		fmt.Fprintf(os.Stderr,
			"Your config does not mention %s, which %s sets.\n"+
				"Taking the system answers as the defaults below; what you write is yours.\n\n",
			strings.Join(masked, ", "), sysPath)
	}

	priorHosts := c.Hosts
	if priorHosts == nil {
		priorHosts = map[string]config.Host{}
	}
	c.Hosts = map[string]config.Host{}

	r := bufio.NewReader(os.Stdin)
	c.Network = ask(r, "network", cmp.Or(c.Network, "namenlos"))
	nodes := config.ListNodes(repo, c.Network)
	if len(nodes) == 0 {
		fmt.Fprintf(os.Stderr, "kno setup: no nodes for network %q (is KNO_REPO set?)\n", c.Network)
		os.Exit(1)
	}

	hn, _ := os.Hostname()
	guess := matchNode(nodes, hn)
	ops := config.LoadOperators(repo, c.Network)
	onames := make([]string, 0, len(ops))
	for n := range ops {
		onames = append(onames, n)
	}
	sort.Strings(onames)

	prior := ""
	if len(c.Nodes) > 0 {
		prior = c.Nodes[0]
	}
	priorNodes := c.Nodes
	thisNode := guess
	if thisNode == "" {
		thisNode = prior
	}
	c.Nodes = nil

	modeDef := "2"
	if thisNode != "" && !isOperatorConfig(priorNodes, priorHosts) {
		modeDef = "1"
	}
	mode := ask(r, "set up (1) this host's node, or (2) all nodes you operate from here?", modeDef)
	operator := mode == "2" || strings.HasPrefix(strings.ToLower(mode), "op") || strings.HasPrefix(strings.ToLower(mode), "all")

	if !operator {
		fmt.Fprintln(os.Stderr, "nodes in this network:")
		for i, n := range nodes {
			mark := ""
			if n == thisNode {
				mark = "   <- this host"
			}
			fmt.Fprintf(os.Stderr, "  %2d) %s%s\n", i+1, n, mark)
		}
		node := pickNodeDef(r, nodes, "which node does THIS host run? (number or name)", thisNode)
		c.Nodes = []string{node}
		sched := ask(r, "keep it updated with (timer/cron/none)", cmp.Or(priorHosts[node].Scheduler, "none"))

		client := yes(ask(r, "run the katzenpost client daemon (kpclientd) here?",
			clientDefault(priorHosts, node)))
		cu := ""
		if client {
			cu = clientUser(r, priorHosts[node].KpclientdUser)
		}
		c.Hosts[node] = thisHost(priorHosts[node], hn, sched, client, cu)
	} else {
		if len(onames) > 0 {

			guessOp := matchOperator(ops, onames, priorNodes)
			for _, name := range onames {
				if guessOp != "" {
					break
				}
				for _, nd := range ops[name] {
					if nd == thisNode && thisNode != "" {
						guessOp = name
					}
				}
			}
			fmt.Fprintln(os.Stderr, "operators, and the services each group runs:")
			for i, name := range onames {
				mark := ""
				if name == guessOp {
					mark = "   <- you?"
				}
				svcs := config.ClaimedServices(repo, c.Network, ops[name])
				for _, ln := range rosterEntry(i+1, name, mark, ops[name], svcs) {
					fmt.Fprintln(os.Stderr, ln)
				}
			}
			who := ask(r, "which operator are you? (number/name; blank to pick nodes yourself)", guessOp)
			for i, name := range onames {
				if who != "" && (who == name || who == fmt.Sprintf("%d", i+1)) {
					for _, nd := range ops[name] {
						c.Nodes = addUniq(c.Nodes, nd)
					}
				}
			}
		}
		if len(c.Nodes) == 0 {
			fmt.Fprintln(os.Stderr, "nodes in this network:")
			for i, n := range nodes {
				fmt.Fprintf(os.Stderr, "  %2d) %s\n", i+1, n)
			}
			c.Nodes = addUniq(c.Nodes, pickNode(r, nodes))
			for yes(ask(r, "organize another node?", "y")) {
				c.Nodes = addUniq(c.Nodes, pickNode(r, nodes))
			}
		}
		fmt.Fprintf(os.Stderr, "organizing: %s\n", strings.Join(c.Nodes, " "))

		svcs := config.ClaimedServices(repo, c.Network, c.Nodes)
		place := map[string]string{}
		for _, s := range svcs {
			place[s] = ask(r, "host label for "+s, cmp.Or(priorLabel(priorHosts, s), nodeOf(s)))
		}
		labels := []string{}
		for _, l := range place {
			labels = addUniq(labels, l)
		}
		sort.Strings(labels)

		where := map[string][2]string{}
		for _, l := range labels {
			ssh, hnl := askHost(r, l, priorHosts[l])
			where[l] = [2]string{ssh, hnl}
		}
		c.Defaults = groupDefaults(r, c.Defaults)
		odd := exceptions(r, labels, c.Defaults, priorHosts)
		for _, l := range labels {
			var hs []string
			for _, s := range svcs {
				if place[s] == l {
					hs = append(hs, s)
				}
			}
			c.Hosts[l] = placedHost(priorHosts[l], odd, l, where[l][0], where[l][1], hs)
		}
	}

	c.Update.Policy = ask(r, "update policy (off/simulate/apply)", cmp.Or(c.Update.Policy, "simulate"))
	c.Update.RequireSignedTag = yes(ask(r, "require a signed tag?", boolDef(c.Update.RequireSignedTag)))
	remote := false
	for _, h := range c.Hosts {
		if h.SSH != "" {
			remote = true
		}
	}
	if remote {
		c.Push.Mode = ask(r, "push mode (copy/install/full)", cmp.Or(c.Push.Mode, "full"))
	} else {
		c.Push.Mode = cmp.Or(c.Push.Mode, "full")
	}

	fmt.Fprintln(os.Stderr, "\n== summary ==")
	fmt.Fprintf(os.Stderr, "  local config -> %s\n", dest)
	fmt.Fprintf(os.Stderr, "  claim: %s   policy=%s signed_tag=%v push=%s\n",
		strings.Join(c.Nodes, " "), c.Update.Policy, c.Update.RequireSignedTag, c.Push.Mode)
	names := []string{}
	for n := range c.Hosts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		h := c.Hosts[n]
		where := h.SSH
		if where == "" {
			where = "this host"
		}

		kpc := ""
		if c.RunsKpclientd(n) {
			kpc = " kpclientd=yes"
			if u := c.KpclientdUser(n); u != "" {
				kpc = " kpclientd=" + u
			}
		}
		priv := ""
		if p := c.Privilege(n); p != config.PrivNone {
			priv = " privilege=" + p
		}
		fmt.Fprintf(os.Stderr, "  host %s: %s scheduler=%s%s%s services=[%s]\n",
			n, where, c.Scheduler(n), kpc, priv, strings.Join(config.HostServices(repo, c, h), " "))
	}
	for _, w := range config.PlacementIssues(repo, c) {
		fmt.Fprintf(os.Stderr, "  WARN: %s\n", w)
	}
	fmt.Fprintln(os.Stderr, sharedRepoNote)
	fmt.Fprintln(os.Stderr, "")
	if !yes(ask(r, "write this config?", "y")) {
		fmt.Fprintln(os.Stderr, "aborted; nothing written")
		os.Exit(1)
	}

	if err := config.Write(c, dest); err != nil {
		fmt.Fprintf(os.Stderr, "kno setup: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "wrote %s\n", dest)

	for _, n := range names {
		h := c.Hosts[n]
		if h.Scheduler == "" || h.Scheduler == "none" {
			continue
		}
		tgt := "this host"
		if h.SSH != "" {
			tgt = h.SSH
		}
		if yes(ask(r, fmt.Sprintf("install the %s scheduler on %s now (needs sudo)?", h.Scheduler, tgt), "n")) {
			if err := knoSchedule(h.Scheduler, h.SSH); err != nil {
				fmt.Fprintf(os.Stderr, "  scheduler install failed: %v\n", err)
			}
			if h.SSH == "" {
				if err := sudoInstallConfig(dest); err != nil {
					fmt.Fprintf(os.Stderr, "  could not install %s to %s: %v\n", dest, systemConfigPath, err)
				} else {
					fmt.Fprintf(os.Stderr, "  installed config to %s (the updater user reads it there)\n", systemConfigPath)
				}
			}
		} else {
			cmd := "kno schedule " + h.Scheduler
			if h.SSH != "" {
				cmd += " " + h.SSH
			}
			fmt.Fprintf(os.Stderr, "  later, on/for %s: %s\n", n, cmd)
			if h.SSH == "" {
				fmt.Fprintf(os.Stderr, "  and (so the updater user sees it): %smkdir -p %s && %sinstall -m 0644 %s %s\n",
					sudoPrefix(), filepath.Dir(systemConfigPath), sudoPrefix(), dest, systemConfigPath)
			}
		}
	}
	fmt.Fprintln(os.Stderr, "done.")
	fmt.Fprintln(os.Stderr, "  check health:  kno status")
	fmt.Fprintln(os.Stderr, "  deploy:        kno push <host-label> <tag>")
}

func boolDef(b bool) string {
	if b {
		return "y"
	}
	return "n"
}
