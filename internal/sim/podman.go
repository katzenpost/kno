package sim

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/parallel"
)

const runnerPackages = "coreutils iproute2 iptables iputils-ping netcat-openbsd ca-certificates procps systemd"

func runnerContainerfile(base string) string {
	return "FROM " + base + "\n" +
		"RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends " +
		runnerPackages + " && rm -rf /var/lib/apt/lists/*\n"
}

func podmanCmd(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, compile.Podman(), args...)
}

func podmanRun(args ...string) (string, error) {
	out, err := podmanCmd(context.Background(), args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("podman %s: %v: %s",
			strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func podmanQuiet(args ...string) error {
	_, err := podmanRun(args...)
	return err
}

func (r *run) podmanStream(args ...string) error {
	cmd := podmanCmd(context.Background(), args...)
	cmd.Stdout, cmd.Stderr = r.Out, r.Out
	return cmd.Run()
}

func (r *run) execIn(ctr, script string) error {
	return podmanQuiet("exec", ctr, "sh", "-c", script)
}

func (r *run) execDetached(ctr, script string) error {
	return podmanQuiet("exec", "-d", "-e", r.epochEnv(), ctr, "sh", "-c", script)
}

func (r *run) podmanDown() error {
	out, err := podmanCmd(context.Background(), "ps", "-aq", "--filter", "label="+r.Net+"=1").Output()
	if err != nil {
		return fmt.Errorf("podman ps: %w", err)
	}
	if ids := strings.Fields(string(out)); len(ids) > 0 {
		if err := podmanQuiet(append([]string{"rm", "-f", "-t", "1"}, ids...)...); err != nil {
			return err
		}
	}
	if out, err := podmanRun("network", "rm", r.Net); err != nil && !noSuchNetwork(out) {
		return err
	}
	return nil
}

func noSuchNetwork(out string) bool {
	o := strings.ToLower(out)
	return strings.Contains(o, "network not found") || strings.Contains(o, "no such network")
}

func runnerImage(p platform) string {
	sum := sha256.Sum256([]byte(runnerPackages))
	return "localhost/kno-sim-" + compile.OSToken(p.OS) + "-" + p.Goarch + ":" + hex.EncodeToString(sum[:4])
}

func (r *run) buildImage(p platform) error {
	return r.buildFrom("runner", runnerImage(p), p, runnerContainerfile)
}

func (r *run) buildFrom(kind, img string, p platform, containerfile func(string) string) error {
	if podmanQuiet("image", "exists", img) == nil {
		return nil
	}
	base, err := compile.BaseImageFor(p.Goarch, p.OS)
	if err != nil {
		return err
	}
	r.log.Logf("building the %s image %s from %s", kind, img, base)
	cmd := podmanCmd(context.Background(), "build", "--platform", "linux/"+p.Goarch,
		"-t", img, "-f", "-")
	cmd.Stdin = strings.NewReader(containerfile(base))
	var errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &errb, &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s image %s failed: %v\n%s", kind, img, err, errb.String())
	}
	return nil
}

func (r *run) installedImage(p platform) (string, error) {
	deb, err := r.debFor(p)
	if err != nil {
		return "", err
	}
	sum, _, err := fsutil.SHA256File(filepath.Join(r.pkgs, deb))
	if err != nil {
		return "", err
	}
	img := "localhost/kno-sim-" + compile.OSToken(p.OS) + "-" + p.Goarch +
		"-kp:" + sum[:12]
	if podmanQuiet("image", "exists", img) == nil {
		return img, nil
	}
	r.log.Logf("installing %s into the image %s", deb, img)
	cmd := podmanCmd(context.Background(), "build",
		"--platform", "linux/"+p.Goarch, "-t", img, "-f", "-", r.pkgs)
	cmd.Stdin = strings.NewReader("FROM " + runnerImage(p) + "\n" +
		"RUN --mount=type=bind,source=" + deb + ",target=/tmp/kp.deb" +
		" dpkg -i /tmp/kp.deb\n")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("dpkg -i %s on %s: %v\n%s", deb, p, err, out.String())
	}
	return img, nil
}

func (r *run) startCtr(ctr, img string, p platform) error {
	return podmanQuiet("run", "-d", "--name", ctr, "--hostname", ctr,
		"--platform", "linux/"+p.Goarch,
		"--network", r.Net, "--cap-add", "NET_ADMIN", "--label", r.Net+"=1",
		"-v", compile.MustMount(r.bin, "/simbin:ro"),
		"-v", compile.MustMount(r.pkgs, "/pkgs:ro"),
		"-v", compile.MustMount(r.sim, "/sim:ro"),
		"-v", compile.MustMount(r.data, "/data:rw"),
		img, "sleep", "infinity")
}

func (r *run) addRoutes(ctr, myIP string, all []string) {
	if myIP != "" {
		if err := r.execIn(ctr, "ip addr add "+myIP+"/32 dev eth0"); err != nil {
			r.log.Warnf("could not assign %s to %s (mesh may be incomplete)", myIP, ctr)
		}
	}
	var b strings.Builder
	for _, ip := range all {
		if ip == myIP {
			continue
		}
		fmt.Fprintf(&b, "ip route replace %s/32 dev eth0 || true\n", ip)
	}
	if b.Len() > 0 {
		_ = r.execIn(ctr, b.String())
	}
}

func natScript(lines []string) string {
	var b strings.Builder
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) != 4 {
			continue
		}
		annIP, annPort, bindIP, bindPort := f[0], f[1], f[2], f[3]
		if annIP == bindIP && annPort == bindPort {
			continue
		}
		if bindIP == "0.0.0.0" {
			fmt.Fprintf(&b, "iptables -t nat -A PREROUTING -p tcp -d %s --dport %s -j REDIRECT --to-ports %s\n",
				annIP, annPort, bindPort)
			continue
		}
		fmt.Fprintf(&b, "ip addr add %s/32 dev eth0 2>/dev/null || true\n", bindIP)
		fmt.Fprintf(&b, "iptables -t nat -A PREROUTING -p tcp -d %s --dport %s -j DNAT --to-destination %s:%s\n",
			annIP, annPort, bindIP, bindPort)
	}
	return b.String()
}

func (r *run) wireNAT(ctr, base string) error {
	lines, err := readLines(filepath.Join(r.sim, base+".nat"))
	if err != nil {
		return nil
	}
	script := natScript(lines)
	if script == "" {
		return nil
	}
	out, err := podmanRun("run", "--rm", "--network", "container:"+ctr,
		"--cap-add", "NET_ADMIN", "--cap-add", "NET_RAW",
		runnerImage(r.clientPlatform()), "sh", "-ec", script)
	if err != nil {
		return fmt.Errorf("nat for %s on %s: %w\n%s", base, ctr, err, out)
	}
	return nil
}

var (
	reCommand = regexp.MustCompile(`(?i)^[[:space:]]*Command[[:space:]]*=`)
	reConfigC = regexp.MustCompile(`^[[:space:]]*c[[:space:]]*=`)
)

func firstValue(lines []string, re *regexp.Regexp) string {
	for _, l := range lines {
		if re.MatchString(l) {
			if m := reIdent.FindStringSubmatch(l); m != nil {
				return m[1]
			}
			return ""
		}
	}
	return ""
}

func (r *run) wireCouriers() error {
	cfgs, err := filepath.Glob(filepath.Join(r.cfg, "*-pq-servicenode.toml"))
	if err != nil {
		return err
	}
	sort.Strings(cfgs)
	for _, cfg := range cfgs {
		lines, err := readLines(cfg)
		if err != nil {
			return err
		}
		if !containsAny(lines, "CBORPluginKaetzchen") {
			continue
		}
		base := strings.TrimSuffix(filepath.Base(cfg), ".toml")
		name := strings.TrimSuffix(base, "-pq-servicenode")
		ctr := r.ctrForIP(cfgIP(lines))
		binPath := firstValue(lines, reCommand)
		cfgPath := firstValue(lines, reConfigC)
		if binPath == "" || cfgPath == "" {
			return fmt.Errorf("courier on %s declares no Command or c path", base)
		}
		keys := "courier-" + name
		courierBin, err := r.binOf("", roleCourier)
		if err != nil {
			return err
		}
		link := ""
		if binPath != courierBin {
			link = fmt.Sprintf("mkdir -p $(dirname %s) && ln -sf %s %s && ", binPath, courierBin, binPath)
		}
		script := fmt.Sprintf(`%smkdir -p $(dirname %s) /courierdata &&
test -x %s &&
cp /sim/courier.toml %s &&
cp /data/%s/link.private.pem /data/%s/link.public.pem /courierdata/ &&
chmod 700 /courierdata`, link, cfgPath, courierBin, cfgPath, keys, keys)
		if err := r.execIn(ctr, script); err != nil {
			return fmt.Errorf("courier wiring failed on %s: %w", ctr, err)
		}
		r.log.Logf("  courier wired on %s (%s)", ctr, cfgPath)
	}
	return nil
}

type ctrPlan struct {
	Name string
	IP   string
	P    platform
}

func (r *run) containerPlan(svcs []daemon) ([]ctrPlan, error) {
	byName := map[string]ctrPlan{}
	for _, s := range svcs {
		p := r.platformOf(s)
		if prev, ok := byName[s.Ctr]; ok {
			if prev.P != p {
				return nil, fmt.Errorf("container %s holds services of two platforms (%s and %s):"+
					" one address cannot be two machines", s.Ctr, prev.P, p)
			}
			continue
		}
		byName[s.Ctr] = ctrPlan{Name: s.Ctr, IP: s.IP, P: p}
	}
	out := make([]ctrPlan, 0, len(byName)+1)
	for _, c := range byName {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return append(out, ctrPlan{Name: r.kpc, P: r.clientPlatform()}), nil
}

func (r *run) bringUp(svcs []daemon) error {
	if err := r.podmanDown(); err != nil {
		return err
	}
	plan, err := r.containerPlan(svcs)
	if err != nil {
		return err
	}
	if err := podmanQuiet("network", "create", "--internal", r.Net); err != nil {
		return fmt.Errorf("network create failed: %w", err)
	}
	plats := make([]platform, 0, len(plan))
	for _, c := range plan {
		plats = append(plats, c.P)
	}
	if err := r.ensureEmulation(plats); err != nil {
		return err
	}
	imgs := map[platform]string{}
	for _, c := range plan {
		if imgs[c.P] != "" {
			continue
		}
		if err := r.buildImage(c.P); err != nil {
			return err
		}
		img, err := r.installedImage(c.P)
		if err != nil {
			return err
		}
		imgs[c.P] = img
	}
	r.log.Logf("starting %d containers", len(plan))
	tasks := make([]parallel.Task, 0, len(plan))
	for _, c := range plan {
		tasks = append(tasks, func(context.Context) error {
			return r.startCtr(c.Name, imgs[c.P], c.P)
		})
	}
	if err := parallel.Run(parallel.Jobs(), tasks); err != nil {
		return err
	}
	for _, c := range plan {
		r.log.Logf("  %s: %s", c.Name, c.P)
	}
	ips := serviceIPs(svcs)
	mesh := make([]parallel.Task, 0, len(plan))
	for _, c := range plan {
		mesh = append(mesh, func(context.Context) error { r.addRoutes(c.Name, c.IP, ips); return nil })
	}
	if err := parallel.Run(parallel.Jobs(), mesh); err != nil {
		return err
	}
	if r.NoCourier {
		r.log.Logf("courier wiring skipped (--no-courier)")
	} else if err := r.wireCouriers(); err != nil {
		return err
	}
	if err := r.validateAll(svcs); err != nil {
		return err
	}
	r.log.Logf("launching services with %s", r.epochEnv())
	for _, s := range svcs {
		_ = r.execIn(s.Ctr, "mkdir -p /data/"+s.Base)
		if err := r.wireNAT(s.Ctr, s.Base); err != nil {
			return err
		}
		cmd := fmt.Sprintf("exec %s >/data/%s/out.log 2>&1", daemonArgs(s), s.Base)
		if err := r.execDetached(s.Ctr, cmd); err != nil {
			return fmt.Errorf("launch %s: %w", s.Base, err)
		}
	}
	r.log.Logf("starting kpclientd")
	if err := r.execDetached(r.kpc, "exec "+r.tool(roleClient)+" -c /sim/client.toml >/data/kpclientd.log 2>&1"); err != nil {
		return err
	}
	return r.checkAlive(svcs)
}

func serviceIPs(svcs []daemon) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range svcs {
		if !seen[s.IP] {
			seen[s.IP] = true
			out = append(out, s.IP)
		}
	}
	sort.Strings(out)
	return out
}

var (
	reEpoch   = regexp.MustCompile(`Epoch: [0-9]+|signed by [0-9]+ directory authorities`)
	reSigners = regexp.MustCompile(`signed by ([0-9]+) directory authorities`)
)

func signers(fetch string) int {
	m := reSigners.FindAllStringSubmatch(fetch, -1)
	if len(m) == 0 {
		return 0
	}
	n := 0
	fmt.Sscan(m[len(m)-1][1], &n)
	return n
}

func authorities(svcs []daemon) int {
	n := 0
	for _, s := range svcs {
		if strings.HasSuffix(s.Base, "-pq-authority") {
			n++
		}
	}
	return n
}

type vanishedError struct {
	Name string
	Poll int
}

func (e vanishedError) Error() string {
	return fmt.Sprintf("the simulation's containers are gone: %s does not exist (seen at poll %d)."+
		" Something removed them after bring-up, so consensus is not the fault."+
		" Another podman workload on this host is the usual cause: a CI runner,"+
		" another sim, or a prune. 'podman events' shows what removed them", e.Name, e.Poll)
}

func (r *run) containerExists(name string) bool {
	return podmanQuiet("container", "inspect", name) == nil
}

func (r *run) fetchArgs() []string {
	return []string{"exec", "-e", r.epochEnv(), r.kpc,
		r.tool(roleFetch), "-f", "/sim/thinclient.toml", "-l", "ERROR"}
}

func (r *run) pollConsensus(tries, want int) error {
	r.log.Logf("polling for a consensus that all %d authorities sign (epoch %s; up to %ds)",
		want, r.Epoch, tries*15)
	best := 0
	started := time.Now()
	for i := 1; i <= tries; i++ {
		if i > 1 {
			time.Sleep(15 * time.Second)
		}
		out, err := podmanCmd(context.Background(), r.fetchArgs()...).CombinedOutput()
		_ = os.WriteFile(filepath.Join(r.Work, "fetch.out"), out, 0o644)
		if err == nil {
			n := signers(string(out))
			best = max(best, n)
			if n >= want {
				r.log.Logf("consensus signed by %d of %d authorities at poll %d after %s", n, want, i, time.Since(started).Round(time.Millisecond))
				for _, m := range reEpoch.FindAllString(string(out), 5) {
					fmt.Fprintln(r.Out, m)
				}
				return nil
			}
			r.log.Logf("  poll %d: consensus signed by %d of %d authorities", i, n, want)
			continue
		}
		if !r.containerExists(r.kpc) {
			return vanishedError{Name: r.kpc, Poll: i}
		}
		r.log.Logf("  poll %d: no consensus yet", i)
	}
	return fmt.Errorf("no consensus signed by all %d authorities after %d polls; the best had %d"+
		" (last fetch in %s)", want, tries, best, filepath.Join(r.Work, "fetch.out"))
}

func (r *run) pingArgs(n int) []string {
	return []string{"exec", "-e", r.epochEnv(), r.kpc, r.tool(rolePing),
		"-c", "/sim/thinclient.toml", "-s", "echo", "--thin", "-n", fmt.Sprint(n)}
}

func (r *run) warmDataplane(tries int) error {
	r.log.Logf("waiting for echo delivery (it settles after the first epoch)")
	for i := 1; i <= tries; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		err := podmanCmd(ctx, r.pingArgs(1)...).Run()
		cancel()
		if err == nil {
			r.log.Logf("data plane ready at attempt %d", i)
			return nil
		}
		time.Sleep(10 * time.Second)
	}
	return fmt.Errorf("data plane not ready after %d attempts (verify and message retry)", tries)
}

func daemonArgs(s daemon) string {
	return s.Bin + " -f /sim/" + s.Base + ".toml"
}

func validateArgs(s daemon) string {
	return daemonArgs(s) + " --validate-only"
}

func (r *run) validateAll(svcs []daemon) error {
	var bad []string
	for _, s := range svcs {
		out, err := podmanRun("exec", "-e", r.epochEnv(), s.Ctr, "sh", "-c", validateArgs(s)+" 2>&1")
		if err != nil {
			bad = append(bad, s.Base+": "+firstError(out))
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("%d config(s) refused by their own daemon:\n  %s", len(bad), strings.Join(bad, "\n  "))
	}
	r.log.Logf("all %d configs pass their own daemon's --validate-only", len(svcs))
	return nil
}

func daemonRunning(ps string, s daemon) bool {
	want := daemonArgs(s)
	for _, l := range strings.Split(ps, "\n") {
		l = strings.TrimSpace(l)
		if l == want || strings.HasSuffix(l, " "+want) {
			return true
		}
	}
	return false
}

func (r *run) checkAlive(svcs []daemon) error {
	time.Sleep(20 * time.Second)
	var dead []string
	for _, s := range svcs {
		out, err := podmanRun("exec", s.Ctr, "ps", "-eo", "args")
		if err == nil && daemonRunning(out, s) {
			continue
		}
		why := ""
		if lines, err := readLines(filepath.Join(r.data, s.Base, "out.log")); err == nil {
			for _, l := range lines[max(0, len(lines)-80):] {
				why += "\n    " + l
			}
		}
		dead = append(dead, s.Base+why)
	}
	if len(dead) > 0 {
		return fmt.Errorf("%d daemon(s) exited after launch:\n  %s", len(dead), strings.Join(dead, "\n  "))
	}
	r.log.Logf("all %d daemons are running", len(svcs))
	return nil
}
