package status

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/katzenpost/kno/internal/fsutil"

	"github.com/katzenpost/kno/internal/attest"
	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/hostsel"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/knover"
	"github.com/katzenpost/kno/internal/layout"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/push"
	"github.com/katzenpost/kno/internal/record"
	"github.com/katzenpost/kno/internal/service"
	"github.com/katzenpost/kno/internal/source"
	"github.com/katzenpost/kno/internal/track"
	"github.com/katzenpost/kno/internal/verify"
)

const dialTimeout = 2 * time.Second

var addrLine = regexp.MustCompile(`(?i)^[[:space:]]*Addresses[[:space:]]*=`)

var bindLine = regexp.MustCompile(`(?i)^[[:space:]]*BindAddresses[[:space:]]*=`)

var tcpAddr = regexp.MustCompile(`tcp://(\[[0-9A-Fa-f:.]+\]|[0-9.]+):([0-9]+)`)

func Run(stdout, stderr io.Writer, args []string) int {
	verbose, live, sources := false, false, false
	only, group := "", ""

	for _, a := range args {
		switch strings.TrimLeft(a, "-") {
		case "h", "help":
			fmt.Fprintln(stderr, "kno status: show the state of each host and service")
			fmt.Fprintln(stderr, "usage: kno status [-v] [-l] [--host LABEL[,LABEL]] [--group NAME]")
			fmt.Fprintln(stderr, "  -v  list every built package (--verbose)")
			fmt.Fprintln(stderr, "  -l  ask each host over ssh (--live)")
			fmt.Fprintln(stderr, "  --sources show managed checkout paths, exact commits and cache freshness")
			fmt.Fprintln(stderr, "  --host   report only these hosts")
			fmt.Fprintln(stderr, "  --group  report only the hosts of an operator group")
			return 0
		case "sources":
			sources = true
		case "v", "verbose":
			verbose = true
		case "l", "live":
			live = true
		}
		if v, ok := strings.CutPrefix(a, "--host="); ok {
			only = v
		}
		if v, ok := strings.CutPrefix(a, "--group="); ok {
			group = v
		}
	}

	for i, a := range args {
		if i+1 >= len(args) {
			break
		}
		switch a {
		case "--host":
			only = args[i+1]
		case "--group":
			group = args[i+1]
		}
	}
	e, err := track.LoadEnv()
	if err != nil {
		fmt.Fprintf(stderr, "kno cannot read the config: %v. Correct kno.toml, or unset KNO_CONFIG\n", err)
		return 1
	}

	if sources {
		writeSourceCaches(stdout, e)
		return 0
	}
	if group != "" {
		labels, err := hostsel.GroupLabels(e, group)
		if err != nil {
			fmt.Fprintf(stderr, "kno status: %v\n", err)
			return 1
		}
		if only != "" {
			labels = append(labels, hostsel.Split(only)...)
		}
		only = strings.Join(labels, ",")
	}
	if only != "" {
		var err error
		if e, err = narrow(e, only); err != nil {
			fmt.Fprintf(stderr, "kno status: %v\n", err)
			return 1
		}
	}

	fmt.Fprintln(stdout, "== kno status ==")
	writeConfig(stdout, e)
	fmt.Fprintf(stdout, "  account: %s\n", selfAccount())

	fmt.Fprintln(stdout, "-- source --")
	network := e.Repo
	if root, ok := config.GitRoot(e.Repo); ok {
		network = root
	}
	fmt.Fprintf(stdout, "  kno checkout:      %s, %s\n", network, gitState(network))
	fmt.Fprintf(stdout, "  signatures:        %s\n", attest.Check(network, "HEAD", attest.SignersPath))
	fmt.Fprintf(stdout, "  katzenpost source: %s. kno build compiles it\n", pinnedKatzenpost(e.Repo))
	if kp := gitState(e.KatzenpostRepo); !strings.HasPrefix(kp, "absent") {
		fmt.Fprintf(stdout, "  katzenpost clone:  %s. kno reads only its tags\n", e.KatzenpostRepo)
	}
	if pin, target := pinnedRef(e.Repo), e.TargetVersion(); pin != "" && target != "" && pin != target {
		fmt.Fprintf(stdout, "  WARN: the target %s is not the katzenpost source %s. kno build"+
			" cannot build %s\n", target, pin, target)
	}

	writeVersions(stdout, e)
	writeServices(stdout, stderr, e, verbose, live)
	if live {
		writeReachability(stdout, e)
	}
	return 0
}

func writeConfig(w io.Writer, e track.Env) {
	if e.ConfigPath == "" {
		fmt.Fprintf(w, "  config:  (no kno.toml; using %s)\n", e.LegacyConf)
		fmt.Fprintf(w, "  network: %s\n", e.Network)
		svcs := ""
		if len(e.Services) > 0 {
			svcs = " " + strings.Join(e.Services, " ")
		}
		fmt.Fprintf(w, "  services:%s\n", svcs)
		return
	}
	var b bytes.Buffer
	config.Summary(&b, e.ConfigPath, e.Config, e.Repo)
	for _, ln := range strings.Split(strings.TrimRight(b.String(), "\n"), "\n") {
		fmt.Fprintf(w, "  %s\n", ln)
	}
	writeMasked(w, e.ConfigPath)
}

func writeMasked(w io.Writer, userPath string) {
	sys := config.SystemPath()
	masked := config.Masked(userPath, sys)
	if len(masked) == 0 {
		return
	}
	fmt.Fprintf(w, "  masking: %s\n", sys)
	fmt.Fprintf(w, "           it sets %s, which this config does not mention\n",
		strings.Join(masked, ", "))
	fmt.Fprintln(w, "           reconcile with: kno setup")
}

func writeVersions(w io.Writer, e track.Env) {
	fmt.Fprintln(w, "-- versions --")

	fmt.Fprintf(w, "  kno:        %s (this host)\n", cli.Version())

	if have, built := localKnoGap(e); have != "" {
		fmt.Fprintf(w, "              this is OLDER than what this checkout builds (%s).\n", built)
		fmt.Fprintln(w, "              It is the binary that deploys every host, so fixes in")
		fmt.Fprintln(w, "              the checkout are not in effect until it is installed:")
		fmt.Fprintln(w, "              sudo dpkg -i "+
			"$(kno build | awk '/\\/kno_/ { print $2; exit }')")
	}

	v := versionsOf(e)
	fmt.Fprintf(w, "  current:    %s\n", orNone(v.current))

	target, held := e.Target()
	switch {
	case target == "" && held != "":
		fmt.Fprintln(w, "  target:     none")
	case target == "":
		fmt.Fprintln(w, "  target:     none: no tag in data/thirdparty/katzenpost-versions is"+
			" also a tag in the katzenpost clone")
	case v.behind != "":
		fmt.Fprintln(w, cli.Column("  target:     ", target+" ("+v.behind+")"))
	case v.atTarget(target):
		fmt.Fprintf(w, "  target:     %s (up to date)\n", target)
	default:
		fmt.Fprintf(w, "  target:     %s (update available)\n", target)
	}

	if held != "" {
		fmt.Fprintf(w, "  held:       %s\n", cli.Wrap(held, cli.Width, "              "))
	}
	fmt.Fprintf(w, "  last-good:  %s\n", orNone(v.lastGood))
	fmt.Fprintf(w, "  quarantine: %s\n", orNone(v.quarantine))
}

type versions struct {
	current, lastGood, quarantine string

	behind string

	hosts int
}

func (v versions) atTarget(tag string) bool { return v.current == tag && v.behind == "" }

func versionsOf(e track.Env) versions {
	labels := organizedLabels(e)
	if len(labels) == 0 {
		v := versions{current: e.CurrentVersion()}
		v.lastGood = fsutil.FirstLine(filepath.Join(e.StateDir, "last-known-good"))
		v.quarantine = strings.Join(strings.Fields(readFile(e.QuarantineFile)), " ")
		return v
	}

	v := versions{hosts: len(labels)}
	cur, good := map[string][]string{}, map[string][]string{}
	var quarantined, missing []string
	for _, l := range labels {
		dir := knopath.HostState(e.StateDir, l)
		c := fsutil.FirstLine(filepath.Join(dir, "current"))
		if c == "" {
			missing = append(missing, l)
		} else {
			cur[c] = append(cur[c], l)
		}
		if g := fsutil.FirstLine(filepath.Join(dir, "last-known-good")); g != "" {
			good[g] = append(good[g], l)
		}
		for _, t := range strings.Fields(readFile(filepath.Join(dir, "quarantine"))) {
			quarantined = append(quarantined, l+":"+t)
		}
	}
	v.current = summarize(cur, len(labels))
	v.lastGood = summarize(good, len(labels))
	v.quarantine = strings.Join(quarantined, " ")
	if len(missing) > 0 {
		v.behind = strings.Join(missing, " ") + " not deployed from here"
	}
	return v
}

func organizedLabels(e track.Env) []string {
	if len(e.Services) > 0 || len(e.Config.Hosts) == 0 {
		return nil
	}
	out := make([]string, 0, len(e.Config.Hosts))
	for l := range e.Config.Hosts {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

func summarize(byVersion map[string][]string, total int) string {
	if len(byVersion) == 0 {
		return ""
	}
	if len(byVersion) == 1 {
		for ver, hosts := range byVersion {
			if len(hosts) == total {
				return ver
			}
			return fmt.Sprintf("%s (%s)", ver, strings.Join(hosts, " "))
		}
	}
	vers := make([]string, 0, len(byVersion))
	for ver := range byVersion {
		vers = append(vers, ver)
	}
	sort.Strings(vers)
	parts := make([]string, 0, len(vers))
	for _, ver := range vers {
		parts = append(parts, fmt.Sprintf("%s (%s)", ver, strings.Join(byVersion[ver], " ")))
	}
	return strings.Join(parts, ", ")
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func writeServices(w, stderr io.Writer, e track.Env, verbose, live bool) {
	reportPackages(w, e, verbose)
	reportHosts(w, e)
	switch {
	case len(e.Services) == 0 && len(e.Managed) > 0:

		var states, problems map[string]string
		var staged map[string][]string
		var idents map[string]Ident
		if live {
			fmt.Fprintf(w, "-- services -- (asked the hosts at %s)\n", time.Now().Format("15:04"))
			states, problems, staged, idents = askHosts(e)
		} else {
			fmt.Fprintln(w, "-- services -- (the state that kno update saw last; -l asks the hosts now)")
			states = recordedStates(e)
		}

		byNode := map[string][]track.ManagedService{}
		for _, m := range e.Managed {
			where := m.Host
			if m.SSH != "" {
				where = m.SSH
			}
			byNode[where] = append(byNode[where], m)
		}
		for _, where := range sortedNodes(byNode) {
			head := "  " + where
			if label := byNode[where][0].Host; label != "" && label != where {
				head = "  " + label + " (" + where + ")"
			}
			if v, ok := runningOf.Load(stateKey(byNode[where][0].Host, "kno")); live && ok {
				head += "  (" + v.(string) + ")"
			}
			fmt.Fprintln(w, head)
			for _, m := range byNode[where] {
				st, ok := states[stateKey(m.Host, m.Service)]
				if !ok {
					st = "-"
				}
				line := fmt.Sprintf("    %-26s %-31s %s", m.Service, unitFor(e, m.Host, m.Service), st)
				if v, ok := runningOf.Load(stateKey(m.Host, m.Service)); live && ok {
					line += "  runs " + v.(string)
				}
				fmt.Fprintln(w, line)
			}
			if why := problems[where]; why != "" {
				fmt.Fprintf(w, "    %s\n", why)
			}

			if line := pinMismatch(e, byNode[where], idents[where]); line != "" {
				fmt.Fprintf(w, "    %s\n", line)
			}

			if n := len(staged[where]); n > 0 {
				list := stagedSummary(staged[where])
				if verbose {
					list = strings.Join(staged[where], " ")
				}
				fmt.Fprintln(w, cli.Column("    staged: ",
					fmt.Sprintf("%d package(s) on disk: %s", n, list)))
			}
		}
	case len(e.Services) == 0:
		fmt.Fprintln(w, "-- services --")
		fmt.Fprintln(w, "  (none selected: no services on this host. To add some, run 'kno setup' or install kno-host-config)")
	case unitStates().Available():
		fmt.Fprintln(w, "-- services --")
		sd := unitStates()
		l := track.Logger{Tool: "status", W: stderr}
		jailed := false
		for _, s := range e.Services {
			unit, err := unitOf(e, s)
			if err != nil {
				l.Errorf("%v", err)
				continue
			}
			jailed = jailed || strings.Contains(unit, "/")
			fmt.Fprintf(w, "  %-26s %-31s %s\n", s, unit, unitState(sd, unit))
		}

		if label, h, ok := config.LocalHost(e.Config); ok && e.Config.RunsKpclientd(label) {
			unit := service.ClientUnit
			if jailed {
				unit = verify.JailUnit(h.KpclientdJail(), service.Client)
			}
			fmt.Fprintf(w, "  %-26s %-31s %s\n", service.Client, unit, unitState(sd, unit))
		}

		reportUserFaults(w, e)
	default:
		fmt.Fprintln(w, "-- services --")
		for _, s := range e.Services {
			fmt.Fprintf(w, "  %s\n", s)
		}
		fmt.Fprintln(w, "  (systemctl not present; states unavailable)")
	}
}

var unitStates = verify.Platform

func writeReachability(w io.Writer, e track.Env) {
	fmt.Fprintln(w, "-- reachability (a TCP connection from this host to each announced address) --")

	probe := e.Services
	if len(probe) == 0 {
		for _, m := range e.Managed {
			probe = append(probe, m.Service)
		}
	}
	if len(probe) == 0 {
		fmt.Fprintln(w, "  (no services)")
		return
	}
	gc := knopath.ConfigsDir(e.Repo, e.Network)
	for _, s := range probe {
		cfg := filepath.Join(gc, s+".toml")
		for _, addr := range addrsOn(cfg, addrLine) {
			state := "reachable"
			if !dials(addr) {
				state = "UNREACHABLE"
				for _, b := range addrsOn(cfg, bindLine) {
					if b != addr && dials(b) {
						state = "no answer from this host, but the service answers on " + b +
							". A host cannot reach its own public address through pf rdr; test it from another machine"
						break
					}
				}
			}
			fmt.Fprintln(w, cli.Column(fmt.Sprintf("  %-32s %s ", s, addr), state))
		}
	}
}

func dials(addr string) bool {
	for try := 0; try < 3; try++ {
		if c, err := net.DialTimeout("tcp", addr, dialTimeout); err == nil {
			c.Close()
			return true
		}
	}
	return false
}

func addrsOn(cfg string, line *regexp.Regexp) []string {
	b, err := os.ReadFile(cfg)
	if err != nil {
		return nil
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if line.MatchString(ln) {
			var out []string
			for _, m := range tcpAddr.FindAllStringSubmatch(ln, -1) {
				out = append(out, m[1]+":"+m[2])
			}
			return out
		}
	}
	return nil
}

func unitOf(e track.Env, svc string) (string, error) {
	if !e.HasService(svc) {
		return "", fmt.Errorf("service '%s' is not in the network topology (configs/%s.toml missing)", svc, svc)
	}
	s, err := service.ResolveService(e.Repo, e.Network, svc)
	if err != nil {
		return "", err
	}
	if s.Unit == "" {
		return svc, nil
	}
	return verify.ServiceUnit(s), nil
}

func unitState(sd verify.Systemd, unit string) string {
	if a := sd.State(unit).Active; a != "" {
		return a
	}
	return "unknown"
}

func gitState(dir string) string {

	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return fmt.Sprintf("absent (%s)", dir)
	}
	if _, ok := track.GitOutput(dir, "rev-parse", "--git-dir"); !ok {
		return fmt.Sprintf("absent (%s)", dir)
	}

	flags := []string{"describe", "--tags", "--always"}
	if deb.WorkingTreeDirty(dir) {
		flags = append(flags, "--dirty")
	}
	desc, ok := track.GitOutput(dir, flags...)
	if !ok {
		desc = "?"
	}
	branch, ok := track.GitOutput(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if !ok {
		branch = "?"
	}
	return fmt.Sprintf("%s (%s)", desc, branch)
}

func reportPackages(w io.Writer, e track.Env, verbose bool) {
	fmt.Fprintln(w, "-- packages --")
	builds := knopath.Builds()
	if len(builds) == 0 {
		fmt.Fprintf(w, "  (none built here; run 'kno build' -- artifacts go to %s)\n",
			knopath.Artifacts())
		return
	}
	inUse := releasesInUse(e)
	var older []string
	for _, b := range builds {
		if !verbose && !inUse[b.Tag] {
			older = append(older, b.Tag)
			continue
		}
		configs, kp, kn, hc := b.Kinds()
		var have []string
		if kp {
			have = append(have, "katzenpost")
		}
		if kn {
			have = append(have, "kno")
		}
		if hc {
			have = append(have, "host-config")
		}
		if configs > 0 {
			have = append(have, fmt.Sprintf("%d config", configs))
		}
		state := "built; kno push did not run for any host"
		if !remoteHosts(e) {
			state = "built"
		}
		if len(b.PushedTo) > 0 {
			state = "kno push ran for " + strings.Join(b.PushedTo, " ")
		}

		lead := fmt.Sprintf("  %-12s %d pkg [%s] ", b.Tag, len(b.Packages),
			strings.Join(have, " "))
		fmt.Fprintln(w, cli.Column(lead, state))
		if verbose {
			for _, p := range b.Packages {
				fmt.Fprintf(w, "    %s\n", p)
			}
		}
	}
	if len(older) > 0 {
		fmt.Fprintf(w, "  %d older build(s) that no host has as installed or last good: %s (-v lists them)\n",
			len(older), strings.Join(older, " "))
	}
	fmt.Fprintf(w, "  in %s\n", knopath.Artifacts())
}

func releasesInUse(e track.Env) map[string]bool {
	in := map[string]bool{}
	if t := e.TargetVersion(); t != "" {
		in[t] = true
	}
	for l := range e.Config.Hosts {
		if e.StateDir == "" {
			break
		}
		d := knopath.HostState(e.StateDir, l)
		for _, f := range []string{"current", "last-known-good"} {
			if v := fsutil.FirstLine(filepath.Join(d, f)); v != "" {
				in[v] = true
			}
		}
	}
	return in
}

var releaseOf = regexp.MustCompile(`_(\d+\.\d+\.\d+)[^_]*_[^_]+\.(deb|pkg)$`)

func stagedSummary(files []string) string {
	count := map[string]int{}
	for _, f := range files {
		r := "other"
		if m := releaseOf.FindStringSubmatch(f); m != nil {
			r = m[1]
		}
		count[r]++
	}
	keys := make([]string, 0, len(count))
	for k := range count {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s (%d)", k, count[k])
	}
	return strings.Join(parts, ", ") + "; -v lists them"
}

type hostState struct {
	label, where, mark, detail string
}

func reportHosts(w io.Writer, e track.Env) {
	if len(e.Config.Hosts) == 0 {
		return
	}
	target := e.TargetVersion()
	labels := make([]string, 0, len(e.Config.Hosts))
	for l := range e.Config.Hosts {
		labels = append(labels, l)
	}
	sort.Strings(labels)

	built := map[string]bool{}
	for _, b := range knopath.Builds() {
		built[b.Tag] = true
	}

	var rows []hostState
	for _, l := range labels {
		h := e.Config.Hosts[l]
		where := h.SSH
		if where == "" {
			where = "this host"
		}
		r := hostState{label: l, where: where}
		r.mark, r.detail = hostLine(e, l, target, pushedTags(l))
		if r.mark == "UPDATE" && target != "" && !built[target] {
			r.detail += ". " + target + " is not built: run kno build"
		}
		rows = append(rows, r)
	}

	fmt.Fprintln(w, "-- hosts --")
	for _, r := range rows {
		lead := fmt.Sprintf("  %-7s %-10s %-24s ", r.mark, r.label, r.where)
		fmt.Fprintln(w, cli.Column(lead, r.detail))

		if v := installedVersions(e, r.label); v != "" {
			fmt.Fprintf(w, "             %s\n", v)
		}
	}
}

func hostLine(e track.Env, l, target string, delivered map[string]bool) (mark, detail string) {
	dir := hostStateDir(e, l)
	tick := hostTick(e, l)
	kv := pendingKV(dir)
	lkg := fsutil.FirstLine(filepath.Join(dir, "last-known-good"))
	quarantined := strings.Fields(readFile(filepath.Join(dir, "quarantine")))
	switch {
	case target == "":
		return "ok", "no target tag"
	case tick.state == "pending" && kv["held"] == "1":
		return "HELD", fmt.Sprintf("has %s, not promoted: the verify window closed at %s,"+
			" and verify said %s. kno update checks it again", kv["tag"],
			when(kv["deadline"]), verdictText(kv["verdict"]))
	case tick.state == "pending":
		parts := []string{"verifying " + kv["tag"]}
		if kv["activated"] != "" {
			parts[0] += ", installed " + when(kv["activated"])
		}
		if kv["checked"] != "" {
			parts = append(parts, "last verify "+when(kv["checked"])+": "+verdictText(kv["verdict"]))
		}
		if lkg == kv["tag"] && lkg != "" {
			parts = append(parts, "promotion after the next passing verify")
		} else if kv["promote"] != "" {
			parts = append(parts, "promotion from "+when(kv["promote"]))
		}
		parts = append(parts, "window closes "+when(kv["deadline"]))
		return "VERIFY", strings.Join(parts, "; ")
	case tick.current != "" && tick.current == target:
		if svcs := configGap(e, l); len(svcs) > 0 {
			return "UPDATE", "has " + target + "; the config packages changed for: " +
				strings.Join(svcs, " ")
		}
		if svcs := failedConfigs(e, l); len(svcs) > 0 {
			return "FAILED", fmt.Sprintf("has %s. The new config packages for %s did not pass"+
				" verify. kno update installs them again only after a commit changes them."+
				" kno doctor --host %s says why", target, strings.Join(svcs, " "), l)
		}
		if smp, _, _ := installedSample(e, l); smp != nil {
			have, want := smp.Packages["kno"], push.KnoVersionFor(e.Repo, target)
			if have != "" && want != "" && deb.CompareVersions(have, want) < 0 {
				return "UPDATE", "has " + target + "; kno update installs kno " + want
			}
		}
		if lkg != target {
			return "FAILED", fmt.Sprintf("has %s, but kno did not promote it. The last good"+
				" release is %s. kno doctor --host %s says why", target, orNone(lkg), l)
		}
		return "ok", "has " + target
	case tick.current != "" && slices.Contains(quarantined, target):
		return "FAILED", fmt.Sprintf("has %s again: %s did not pass verify and is quarantined"+
			" here. kno doctor --host %s says why; kno unquarantine --host %s %s allows it again",
			tick.current, target, l, l, target)
	case tick.current != "":
		return "UPDATE", "has " + tick.current + "; the target is " + target
	case len(delivered) == 0:
		return "UPDATE", "kno has no record that this host installed on it"
	case delivered[target]:
		return "ok", "has " + target
	}
	return "UPDATE", "got " + newest(delivered) + " from this host. The target is " + target
}

func pendingKV(dir string) map[string]string {
	kv := map[string]string{}
	if dir == "" {
		return kv
	}
	for _, line := range strings.Split(readFile(filepath.Join(dir, "pending")), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			kv[k] = v
		}
	}
	return kv
}

func when(secs string) string {
	n, err := strconv.ParseInt(secs, 10, 64)
	if err != nil {
		return "?"
	}
	t := time.Unix(n, 0).Local()
	if y, m, d := time.Now().Date(); t.Year() == y && t.Month() == m && t.Day() == d {
		return t.Format("15:04")
	}
	return t.Format("Jan 2 15:04")
}

func verdictText(code string) string {
	switch code {
	case "0":
		return "passed"
	case "1":
		return "not yet"
	case "2":
		return "FAILED"
	case "3":
		return "no consensus"
	case "4":
		return "cannot ask"
	case "127":
		return "the host did not answer"
	case "":
		return "none yet"
	}
	return "exit " + code
}

func installedSample(e track.Env, label string) (
	*record.Sample, string, error) {
	rec, err := record.LoadPending(hostStateDir(e, label), e.Repo, e.Network, label)
	if err != nil {
		return nil, "", err
	}
	s := rec.Deployed
	note := ""
	if s == nil && rec.Deploying != nil && len(rec.Deploying.Packages) > 0 {
		s, note = &record.Sample{Packages: rec.Deploying.Packages}, "  (installed, in the verify window)"
	}
	if s == nil {
		s, note = rec.Observed, "  (seen before the last install)"
	}
	return s, note, nil
}

func installedVersions(e track.Env, label string) string {
	s, note, err := installedSample(e, label)
	if errors.Is(err, record.ErrMalformed) {
		return fmt.Sprintf("kno cannot read the deployment record (%v)", err)
	}
	if s == nil || len(s.Packages) == 0 {
		return ""
	}
	kp := map[string]bool{}
	pkgs := map[string]string{}
	for name, v := range s.Packages {
		if _, base, jailed := strings.Cut(name, "/"); jailed {
			name = base
		}
		if name == "katzenpost" {
			kp[v] = true
			continue
		}
		pkgs[name] = v
	}
	var parts []string
	if len(kp) > 0 {
		parts = append(parts, "katzenpost "+strings.Join(slices.Sorted(maps.Keys(kp)), " "))
	}
	if v := pkgs["kno"]; v != "" {
		parts = append(parts, "kno "+v)
	}
	cfg := map[string][]string{}
	for name, v := range pkgs {
		if strings.HasPrefix(name, configPkgPrefix) {
			cfg[v] = append(cfg[v], strings.TrimPrefix(name, configPkgPrefix))
		}
	}
	switch len(cfg) {
	case 0:
	case 1:
		for v := range cfg {
			parts = append(parts, "config "+v)
		}
	default:

		for _, v := range slices.Sorted(maps.Keys(cfg)) {
			parts = append(parts, "config "+v+" ("+strings.Join(cfg[v], " ")+")")
		}
	}
	out := strings.Join(parts, "  ")
	if out != "" {
		out += note
	}
	return out
}

const configPkgPrefix = knopath.ConfigPkgPrefix

func remoteHosts(e track.Env) bool {
	for _, h := range e.Config.Hosts {
		if h.SSH != "" {
			return true
		}
	}
	return false
}

func pushedTags(host string) map[string]bool {
	out := map[string]bool{}
	tags, err := os.ReadDir(filepath.Join(knopath.Artifacts(), "pushed"))
	if err != nil {
		return out
	}
	for _, t := range tags {
		if !t.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(knopath.PushReceipts(t.Name()), host+".txt")); err == nil {
			out[t.Name()] = true
		}
	}
	return out
}

func newest(tags map[string]bool) string {
	var all []string
	for t := range tags {
		all = append(all, t)
	}
	sort.Slice(all, func(i, j int) bool { return knopath.VersionLess(all[i], all[j]) })
	return all[len(all)-1]
}

type tickState struct {
	current string
	state   string
}

func hostTick(e track.Env, label string) tickState {
	dir := hostStateDir(e, label)
	if dir == "" {
		return tickState{}
	}
	return tickState{
		current: fsutil.FirstLine(filepath.Join(dir, "current")),
		state:   fsutil.FirstLine(filepath.Join(dir, "state")),
	}
}

func readFile(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

func pinnedKatzenpost(repo string) string {
	pins, err := source.ParsePins(filepath.Join(repo, "thirdparty", "pins.env"))
	if err != nil {
		return "unknown (cannot read thirdparty/pins.env)"
	}
	ref := pins["KATZENPOST_REF"]
	if ref == "" {
		return "unpinned (no KATZENPOST_REF in thirdparty/pins.env)"
	}
	sha := pins["KATZENPOST_SHA"]
	if len(sha) > 12 {
		sha = sha[:12]
	}
	if sha == "" {

		return ref + " (pinned by ref only; no KATZENPOST_SHA)"
	}
	return ref + ", commit " + sha + " (data/thirdparty/pins.env)"
}

func pinnedRef(repo string) string {
	pins, err := source.ParsePins(filepath.Join(repo, "thirdparty", "pins.env"))
	if err != nil {
		return ""
	}
	return pins["KATZENPOST_REF"]
}

func hostStateDir(e track.Env, label string) string {
	if e.StateDir == "" {
		return ""
	}
	if h, ok := e.Config.Hosts[label]; ok && h.SSH == "" {
		return e.StateDir
	}
	return knopath.HostState(e.StateDir, label)
}

func unitFor(e track.Env, label, svc string) string {
	if svc == service.Client {
		return service.ClientUnit
	}
	if s, err := service.ResolveService(e.Repo, e.Network, svc); err == nil && s.Unit != "" {
		return unitOn(e, label, s)
	}
	return "?"
}

func unitOn(e track.Env, label string, s service.Service) string {
	return layout.Unit(s, hostTick(e, label).current)
}

func stateKey(host, svc string) string { return host + "/" + svc }

func sortedNodes(m map[string][]track.ManagedService) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := m[out[i]][0].Host, m[out[j]][0].Host
		if a != b {
			return a < b
		}
		return out[i] < out[j]
	})
	for _, k := range out {
		svcs := m[k]
		sort.SliceStable(svcs, func(i, j int) bool {
			ci, cj := svcs[i].Service == service.Client, svcs[j].Service == service.Client
			if ci != cj {
				return cj
			}
			return svcs[i].Service < svcs[j].Service
		})
	}
	return out
}

func configGap(e track.Env, label string) []string {
	installed, failed := configStamps(e, label, "configs"), configStamps(e, label, "configs-failed")
	if len(installed) == 0 {
		return nil
	}
	var out []string
	for _, svc := range e.Config.Hosts[label].Services {
		want := deb.ConfigStamp(e.Repo, e.Network, svc)
		if installed[svc] != want && (failed[svc] == "" || failed[svc] != want) {
			out = append(out, svc)
		}
	}
	return out
}

func failedConfigs(e track.Env, label string) []string {
	failed := configStamps(e, label, "configs-failed")
	var out []string
	for _, svc := range e.Config.Hosts[label].Services {
		if failed[svc] != "" && failed[svc] == deb.ConfigStamp(e.Repo, e.Network, svc) {
			out = append(out, svc)
		}
	}
	return out
}

func configStamps(e track.Env, label, file string) map[string]string {
	if _, ok := e.Config.Hosts[label]; !ok || e.StateDir == "" {
		return nil
	}
	out := map[string]string{}
	for _, kv := range strings.Fields(readFile(filepath.Join(knopath.HostState(e.StateDir, label), file))) {
		svc, stamp, _ := strings.Cut(kv, "=")
		out[svc] = stamp
	}
	return out
}

func pinMismatch(e track.Env, svcs []track.ManagedService, id Ident) string {
	if id.Arch == "" && id.OS == "" {
		return ""
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range svcs {

		if m.Service == service.Client {
			continue
		}
		node := push.NodeOf(m.Service)
		if seen[node] {
			continue
		}
		seen[node] = true
		arch, os := config.NodeArch(e.Repo, e.Network, node), config.NodeOS(e.Repo, e.Network, node)
		var bad []string
		if id.Arch != "" && arch != id.Arch {
			bad = append(bad, fmt.Sprintf("arch %s, reports %s", arch, id.Arch))
		}
		if id.OS != "" && os != id.OS {
			bad = append(bad, fmt.Sprintf("os %s, reports %s", os, id.OS))
		}
		if len(bad) > 0 {
			out = append(out, fmt.Sprintf("machines/%s.toml gives %s, and the host reports otherwise: %s", node, node, strings.Join(bad, "; ")))
		}
	}
	return strings.Join(out, "; ")
}

func recordedStates(e track.Env) map[string]string {
	out := map[string]string{}
	for label := range e.Config.Hosts {
		rec, err := record.LoadPending(hostStateDir(e, label), e.Repo, e.Network, label)
		if err != nil {
			continue
		}
		s := rec.Deployed
		if s == nil {
			s = rec.Observed
		}
		if s == nil {
			continue
		}

		for _, svc := range rec.Services {
			unit := unitFor(e, label, svc)
			if st, ok := s.Units[unit]; ok {
				out[stateKey(label, svc)] = st
			} else if sv, err := service.ResolveService(e.Repo, e.Network, svc); err == nil && sv.LegacyUnit != "" {
				if st, ok := s.Units[sv.LegacyUnit]; ok {
					out[stateKey(label, svc)] = st
				}
			}
		}
		if st, ok := s.Units[service.ClientUnit]; ok {
			out[stateKey(label, service.Client)] = st
		}
	}

	for label := range e.Config.Hosts {
		for svc, st := range ObservedStates(label, func(svc string) string { return unitFor(e, label, svc) }) {
			out[stateKey(label, svc)] = st
		}
	}
	return out
}

func narrow(e track.Env, only string) (track.Env, error) {
	want := map[string]bool{}
	for _, l := range strings.Split(only, ",") {
		if l = strings.TrimSpace(l); l != "" {
			want[l] = true
		}
	}
	if len(want) == 0 {
		return e, nil
	}
	hosts := map[string]config.Host{}
	var svcs []string
	for label, h := range e.Config.Hosts {
		if !want[label] {
			continue
		}
		hosts[label] = h
		svcs = append(svcs, config.HostServices(e.Repo, e.Config, h)...)
		delete(want, label)
	}
	if len(want) > 0 {
		var missing []string
		for l := range want {
			missing = append(missing, l)
		}
		sort.Strings(missing)
		return e, fmt.Errorf("no such host in %s: %s", e.ConfigPath, strings.Join(missing, ", "))
	}
	sort.Strings(svcs)
	e.Config.Hosts = hosts

	var managed []track.ManagedService
	for _, m := range e.Managed {
		if _, ok := hosts[m.Host]; ok {
			managed = append(managed, m)
		}
	}
	e.Managed = managed

	if label, _, ok := config.LocalHost(e.Config); ok && len(hosts) > 0 {
		_ = label
	} else {
		e.Services = nil
	}
	if len(e.Services) > 0 {
		e.Services = svcs
	}
	return e, nil
}

func localKnoGap(e track.Env) (have, built string) {
	have = cli.Version()
	if have == "" || strings.HasPrefix(have, "(") {
		return "", ""
	}
	tag := e.TargetVersion()
	if tag == "" || e.Repo == "" {
		return "", ""
	}
	built = knover.Version(e.Repo, tag)
	if built == "" || built == have {
		return "", ""
	}

	if knopath.VersionLess(have, built) {
		return have, built
	}
	return "", ""
}

func selfAccount() string {
	uid, gid := os.Getuid(), os.Getgid()
	name := strconv.Itoa(uid)
	if u, err := user.LookupId(name); err == nil {
		name = u.Username
	}
	out := fmt.Sprintf("%s (uid=%d gid=%d)", name, uid, gid)
	if s := os.Getenv("SUDO_USER"); s != "" && uid == 0 {
		out = fmt.Sprintf("%s (uid=%d gid=%d, via sudo from %s)", name, uid, gid, s)
	}
	return out
}

func writeSourceCaches(w io.Writer, e track.Env) {
	for _, entry := range []struct{ name, dir string }{{"kno", e.KnoSourceDir()}, {"katzenpost", filepath.Join(e.StateDir, "katzenpost-source")}, {"hpqc", filepath.Join(e.StateDir, "hpqc-source")}} {
		fmt.Fprintf(w, "%s checkout: %s\n", entry.name, entry.dir)
		selected, ok := track.CachedSource(entry.dir)
		if !ok {
			fmt.Fprintln(w, "  no cached selection. Use kno build <target> --refresh")
			continue
		}
		fmt.Fprintf(w, "  remote: %s\n  ref: %s\n  commit: %s\n  %s\n", selected.URL, selected.Ref, selected.Commit, track.SourceFreshness(selected))
	}
}
