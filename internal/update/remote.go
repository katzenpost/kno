package update

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/katzenpost/kno/internal/argcheck"
	"github.com/katzenpost/kno/internal/build"
	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/hostsel"
	"github.com/katzenpost/kno/internal/knopath"
	preflightpkg "github.com/katzenpost/kno/internal/preflight"
	"github.com/katzenpost/kno/internal/push"
	"github.com/katzenpost/kno/internal/record"
	"github.com/katzenpost/kno/internal/sshx"
	"github.com/katzenpost/kno/internal/track"
	"github.com/katzenpost/kno/internal/verify"
)

type remoteHost struct {
	Label    string
	Node     string
	Services []string
}

func stateReady(l track.Logger, out io.Writer, e track.Env, target string, hosts []remoteHost) bool {
	ts := make([]preflightpkg.Target, 0, len(hosts))
	for _, h := range hosts {
		if hostEnv(e, h).Quarantined(target) {
			continue
		}
		ts = append(ts, preflightpkg.Target{Label: h.Label, SSH: h.Node, Services: h.Services,
			Client: e.Config.RunsKpclientd(h.Label)})
	}
	if len(ts) == 0 {
		return true
	}
	reports, err := preflightpkg.CheckIn(e.Repo, e.Network, target, e.StateDir, e.ReleasesDir, ts)
	if err != nil {
		l.Warnf("state preflight could not run: %v", err)
		return true
	}
	blocked := false
	for _, r := range reports {
		if r.Blocked() {
			blocked = true
		}
	}
	if !blocked {
		return true
	}
	l.Errorf("kno does not upgrade: a host is in a state that %s does not accept,"+
		" or one a failed install could not be undone from", target)
	preflightpkg.ReportAll(out, reports)
	l.Logf("  Nothing was changed. 'kno preflight' repeats this check, and")
	l.Logf("  --force proceeds anyway.")
	return false
}

func organizedHosts(e track.Env) []remoteHost {
	var out []remoteHost
	for _, label := range config.ClaimedHosts(e.Repo, e.Config) {
		h, ok := e.Config.Hosts[label]
		if !ok {
			continue
		}
		svcs := config.HostServices(e.Repo, e.Config, h)
		if len(svcs) == 0 {
			continue
		}
		node := h.SSH
		if node == "" {
			node = label
		}
		out = append(out, remoteHost{Label: label, Node: node, Services: svcs})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

func hostEnv(e track.Env, h remoteHost) track.Env {
	he := e
	he.Services = h.Services
	he.StateDir = knopath.HostState(e.StateDir, h.Label)
	he.QuarantineFile = filepath.Join(he.StateDir, "quarantine")
	he.CurrentFile = filepath.Join(he.StateDir, "current")
	return he
}

type stage struct {
	mu      sync.Mutex
	env     track.Env
	hosts   []remoteHost
	log     track.Logger
	verbose bool
	done    bool
	tag     string
	err     error
}

func (s *stage) ensure(tag string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done && s.tag == tag {
		return s.err
	}
	s.done, s.tag = true, tag
	o := s.options(tag)
	if len(o.Arches) == 0 {
		s.err = fmt.Errorf("no architecture resolved for %d service(s)", len(o.Services))
		return s.err
	}
	s.log.Logf("checking that %s is built for %d host(s): %d service(s), %s",
		tag, len(o.Hosts), len(o.Services), strings.Join(o.Arches, " "))
	if _, err := build.Ready(o); errors.Is(err, build.ErrNotBuilt) {
		s.err = fmt.Errorf("no build of %s matches this kno and the hosts %s. A build is for one"+
			" kno commit and one set of hosts. Run 'kno build', then run kno update again",
			tag, strings.Join(o.Hosts, " "))
	} else {
		s.err = err
	}
	return s.err
}

func (s *stage) options(tag string) build.Options {
	var labels []string
	seen := map[string]bool{}
	var svcs []string
	for _, h := range s.hosts {
		labels = append(labels, h.Label)
		for _, v := range h.Services {
			if !seen[v] {
				seen[v] = true
				svcs = append(svcs, v)
			}
		}
	}
	sort.Strings(svcs)
	cfg := s.env.Config
	formats := build.BackendFormats(s.env.Repo, s.env.Network, svcs)
	return build.Options{
		Repo:    s.env.Repo,
		Network: s.env.Network,
		Tag:     tag,

		Dist:        filepath.Dir(s.env.ReleasesDir),
		Services:    svcs,
		Arches:      build.ArchesForServices(s.env.Repo, s.env.Network, svcs),
		Hosts:       labels,
		Config:      &cfg,
		SkipFreeBSD: !formats["freebsd"],
		SkipDeb:     !formats["deb"],
		SkipDocker:  true,
		Verbose:     s.verbose,
	}
}

type RemoteRunner struct {
	Env   track.Env
	Host  remoteHost
	Mode  string
	SSH   string
	Dry   bool
	Stage *stage
	Log   track.Logger
	Out   io.Writer

	Force bool

	Before *record.Sample

	verified *int

	pushErr error

	since string

	Quiet bool
	Last  string
}

func (r *RemoteRunner) SetSince(v string) {
	if t, ok := strings.CutPrefix(v, "@"); ok {
		if _, err := strconv.ParseInt(t, 10, 64); err == nil {
			r.since = v
		}
	}
}

func (r *RemoteRunner) Track() (string, int) {
	target, why := r.Env.Target()
	if target == "" {
		r.Log.Errorf("%s: %s", r.Host.Label, noTarget(why))
		return "", 1
	}
	if why != "" {
		r.Log.Warnf("%s: %s", r.Host.Label, why)
	}
	if target == r.Env.CurrentVersion() && !r.Force {
		rc, msg := sameTag(target, r.Configs(), readLine(configsFile(r.Env.StateDir)),
			readLine(failedConfigsFile(r.Env.StateDir)))
		r.Log.Logf("%s: %s", r.Host.Label, msg)
		return target, rc
	}
	return target, 0
}

func (r *RemoteRunner) Build(tag string) error { return r.Stage.ensure(tag) }

func (r *RemoteRunner) Activate(tag, rel string) error {
	r.pushErr = nil
	if rc := push.Run(r.Out, r.Host.Label, tag, r.Mode, r.Dry); rc != 0 {
		r.pushErr = fmt.Errorf("push %s to %s failed (%d)", tag, r.Host.Label, rc)
		return r.pushErr
	}
	return nil
}

func (r *RemoteRunner) Verify(rel string, consensusOnly bool) int {
	cmd := "VERIFY_CHECK_PARTICIPATION=" + participation() + " kno verify"
	if r.since != "" && !consensusOnly {
		cmd = "VERIFY_SINCE=" + r.since + " " + cmd
	}
	if consensusOnly {
		cmd = "VERIFY_CONSENSUS_ONLY=1 " + cmd
	}
	out, code, err := r.ssh(cmd)
	switch {
	case err != nil:
		r.Log.Errorf("%s: could not run verify over ssh (%v)", r.Host.Label, err)
		code = VerifyUnavailable
	case code == sshTransportFailure:
		r.Log.Errorf("%s: ssh cannot connect to %s. kno has no result for this host",
			r.Host.Label, r.Host.Node)
		code = VerifyUnavailable
	case code == 127:
		r.Log.Errorf("%s: ssh found no kno command on %s (exit 127). Install kno on %s",
			r.Host.Label, r.Host.Node, r.Host.Label)
		code = VerifyUnavailable
	}
	r.verified = &code
	if code == VerifyUnavailable {
		return code
	}

	if (!consensusOnly || code != 0) && (!r.Quiet || code >= 2 || strconv.Itoa(code) != r.Last) {

		showRemote(r.Out, r.Host.Label, out)
	}
	return code
}

const sshTransportFailure = 255

const defaultSSHDeadline = 5 * time.Minute

func sshDeadline() time.Duration {
	if v := os.Getenv("KNO_SSH_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return defaultSSHDeadline
}

const queryDeadline = 90 * time.Second

func (r *RemoteRunner) query(remote string) (string, int, error) {
	return r.sshWithin(queryDeadline, remote)
}

func (r *RemoteRunner) ssh(remote string) (string, int, error) {
	return r.sshWithin(sshDeadline(), remote)
}

func (r *RemoteRunner) sshWithin(d time.Duration, remote string) (string, int, error) {
	if err := argcheck.Target("ssh target", r.Host.Node); err != nil {
		return "", 0, err
	}
	argv := sshx.SSH(r.SSH, r.Host.Node, remote)

	argv = append(argv[:1:1], append([]string{"-o", "ConnectTimeout=10"}, argv[1:]...)...)
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	c := exec.CommandContext(ctx, argv[0], argv[1:]...)
	b, err := c.CombinedOutput()
	if ctx.Err() != nil {
		return string(b), 0, fmt.Errorf("no answer from %s within %s."+
			" If this repeats, KNO_SSH_MULTIPLEX=0 turns off the shared"+
			" connection and KNO_SSH_TIMEOUT changes the wait",
			r.Host.Node, d)
	}
	if err == nil {
		return string(b), 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return string(b), ee.ExitCode(), nil
	}
	return string(b), 0, err
}

func showRemote(w io.Writer, label, out string) {
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fmt.Fprintf(w, "  %s| %s\n", label, line)
	}
}

func RunRemoteGroup(out io.Writer, e track.Env, v Flags) int {
	group := v.Group
	if group != "" {
		labels, err := hostsel.GroupLabels(e, group)
		if err != nil {
			track.Logger{Tool: "update", W: out}.Errorf("%v", err)
			return 1
		}
		if v.Hosts != "" {
			labels = append(labels, strings.Split(v.Hosts, ",")...)
		}
		v.Hosts = strings.Join(labels, ",")
	}
	return runRemote(out, e, v)
}

func runRemote(out io.Writer, e track.Env, v Flags) int {
	l := track.Logger{Tool: "update", W: out}
	hosts := organizedHosts(e)
	if len(hosts) == 0 {
		l.Errorf("this organizer has no host that carries a service. Run 'kno setup'")
		return 1
	}
	if v.Hosts != "" {
		var err error
		if hosts, err = selectHosts(hosts, v.Hosts); err != nil {
			l.Errorf("%v", err)
			return 1
		}
	}
	v, apply := remotePolicy(e, v)
	if !apply {
		l.Logf("update policy is 'off'. kno applies no update")
		return 0
	}
	dry, force := v.Dry, v.Force

	if cmp.Or(os.Getenv("KNO_TRACK_FETCH"), "1") == "1" {
		if err := e.EnsureRepos(l, true); err != nil {
			l.Errorf("%v", err)
			return 1
		}
	}
	target, why := e.Target()
	if target == "" {
		l.Errorf("%s", noTarget(why))
		return 1
	}

	mode := e.PushMode
	if mode == "" {
		mode = "full"
	}
	l.Logf("target %s for %d host(s), push mode %s", target, len(hosts), mode)
	if force {
		l.Logf("--force is set: kno installs again also on a host that has %s", target)
	}

	st := &stage{env: e, hosts: hosts, log: l, verbose: os.Getenv("V") == "1"}

	if !dry {
		fresh := deploying(e, hosts)
		if len(fresh) > 0 {
			if err := st.ensure(target); err != nil {
				l.Errorf("%v", err)
				l.Logf("no host was contacted and nothing was changed")
				return 1
			}
		}
		ok := preflight(l, fresh, target, func(label string) error {
			return push.Preflight(e, label, target, mode)
		})
		if !ok {
			return 1
		}

		if !force && len(fresh) > 0 && !stateReady(l, out, e, target, fresh) {
			return 1
		}
	}

	if dry && !force {
		if fresh := deploying(e, hosts); len(fresh) > 0 && !stateReady(l, out, e, target, fresh) {
			return 1
		}
	}

	if !dry && !v.NoWait {
		return waitHosts(out, l, e, v, hosts, st, mode, target)
	}

	rc := 0
	var pending []string
	verdicts := map[string]int{}
	ts := make([]*ticker, len(hosts))
	codes := make([]int, len(hosts))
	eachHost(out, hosts, func(i int, h remoteHost, w io.Writer) {
		fmt.Fprintf(w, "==> %s (%s)\n", h.Label, h.Node)
		ts[i] = newTicker(w, track.Logger{Tool: "update", W: w}, e, v, h, st, mode, target)
		codes[i] = ts[i].tick(true)
	})
	for i, h := range hosts {
		if codes[i] != 0 {
			rc = 1
		}
		if readLine(filepath.Join(ts[i].he.StateDir, "state")) == "pending" {
			pending = append(pending, h.Label)
		}
		if ts[i].verdict != nil {
			verdicts[h.Label] = *ts[i].verdict
		}
	}
	reportNetworkConsensus(l, verdicts)

	if len(pending) > 0 {
		l.Logf("%d host(s) in the verify window: %s", len(pending), strings.Join(pending, " "))
		l.Logf("  run 'kno update' again to promote them, or to roll back a host" +
			" that does not pass verify before its window closes")
	}
	return rc
}

func remotePolicy(e track.Env, v Flags) (Flags, bool) {
	switch policyOf(e, v) {
	case "off":
		return v, false
	case "simulate":
		v.Dry = true
	}
	return v, true
}

const (
	exitDone      = 0
	exitFailed    = 1
	exitUnsettled = 3
)

func waitHosts(out io.Writer, l track.Logger, e track.Env, v Flags, hosts []remoteHost,
	st *stage, mode, target string) int {
	ts := make([]*ticker, len(hosts))
	codes := make([]int, len(hosts))
	deployed := make([]bool, len(hosts))
	eachHost(out, hosts, func(i int, h remoteHost, w io.Writer) {
		fmt.Fprintf(w, "==> %s (%s)\n", h.Label, h.Node)
		t := makeTicker(w, track.Logger{Tool: "update", W: w}, e, v, h, st, mode, target)
		ts[i] = t
		wasPending := readLine(t.stateFile("state")) == "pending"
		codes[i] = t.step(true)
		deployed[i] = wasPending || readLine(t.stateFile("state")) == "pending"
		if codes[i] == 0 || deployed[i] {
			t.wait()
			t.finish()
		}
	})
	rc := exitDone
	var skipped, unsettled []string
	for i, h := range hosts {
		what, hrc := ts[i].outcome()
		if codes[i] != 0 && hrc == exitDone {
			what, hrc = "the update failed", exitFailed
		}
		switch {
		case hrc == exitDone:
			l.Logf("%s: %s", h.Label, what)
			continue
		case codes[i] == 0 && !deployed[i]:
			l.Errorf("%s: %s", h.Label, what)
			l.Logf("  kno installed nothing on %s", h.Label)
			skipped = append(skipped, h.Label)
			hrc = exitFailed
		default:
			l.Errorf("%s: %s", h.Label, what)
			if hrc == exitUnsettled {
				unsettled = append(unsettled, h.Label)
			}
		}
		rc = worse(rc, hrc)
	}
	if len(skipped) > 0 {
		l.Logf("kno installed nothing on %s. Run kno update again later", strings.Join(skipped, " "))
	}
	if len(unsettled) > 0 {
		l.Logf("  run the same kno update again for %s. It continues from the state on disk", strings.Join(unsettled, " "))
	}
	return rc
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(b)
}

func eachHost(out io.Writer, hosts []remoteHost, f func(i int, h remoteHost, w io.Writer)) {
	shared := &lockedWriter{w: out}
	var wg sync.WaitGroup
	for i, h := range hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := cli.NewPrefixWriter(shared, cli.HostPrefix(h.Label))
			defer func() {
				if c, ok := w.(io.Closer); ok {
					c.Close()
				}
			}()
			f(i, h, w)
		}()
	}
	wg.Wait()
}

func worse(a, b int) int {
	if a == exitFailed || b == exitFailed {
		return exitFailed
	}
	return max(a, b)
}

type ticker struct {
	out    io.Writer
	l      track.Logger
	e, he  track.Env
	h      remoteHost
	v      Flags
	st     *stage
	mode   string
	target string

	now   func() time.Time
	sleep func(time.Duration)
	step  func(first bool) int

	before, last *record.Sample
	verdict      *int
	pushErr      error
}

var makeTicker = newTicker

func newTicker(out io.Writer, l track.Logger, e track.Env, v Flags, h remoteHost,
	st *stage, mode, target string) *ticker {
	t := &ticker{out: out, l: l, e: e, he: hostEnv(e, h), h: h, v: v, st: st,
		mode: mode, target: target, now: time.Now, sleep: time.Sleep}
	t.step = t.tick
	return t
}

func (t *ticker) stateFile(name string) string { return filepath.Join(t.he.StateDir, name) }

func (t *ticker) tick(first bool) int {
	kv, _ := readKV(t.stateFile("pending"))
	runner := &RemoteRunner{
		Env:   t.he,
		Host:  t.h,
		Mode:  t.mode,
		SSH:   sshx.Program(),
		Dry:   t.v.Dry,
		Stage: t.st,
		Force: t.v.Force && first,
		Log:   track.Logger{Tool: "update", W: t.out},
		Out:   t.out,
		Quiet: !first && !t.v.Verbose,
		Last:  kv["verdict"],
	}
	if !t.v.Dry && first {
		t.l.Logf("%s: connecting", t.h.Label)
		if n := sshx.CheckMaster(runner.SSH, t.h.Node); n > 0 {
			t.l.Logf("  cleared %d stale ssh control socket(s)", n)
		}
		t.before = runner.sample("before this deployment")
		t.last = t.before
		if t.before != nil && len(t.before.Packages) > 0 {
			t.l.Logf("%s: runs %s", t.h.Label, packageList(t.before.Packages))
		}
		if t.reconcile(runner) {
			t.before = runner.sample("before this deployment")
			t.last = t.before
		}
	}
	runner.Before = t.before
	wasSteady := readLine(t.stateFile("state")) != "pending"
	if first && !wasSteady && kv["tag"] != "" {
		t.l.Logf("%s: continuing with %s", t.h.Label, kv["tag"])
	}
	code := Run(Options{
		Env:            t.he,
		Policy:         policyOf(t.e, t.v),
		DryRun:         t.v.Dry,
		Force:          t.v.Force && first,
		StallForward:   t.v.StallForward,
		Promote:        t.v.Promote || sameRelease(t.he, t.target),
		Window:         window(),
		Quiet:          !first && !t.v.Verbose,
		NoEarlierBuild: true,
		Now:            t.now,
		Out:            t.out,
		Runner:         runner,
	})
	if code == 0 && !t.v.Dry && wasSteady && knoDiffers(t.he, t.target, t.before) {
		t.l.Logf("%s: has %s and its config packages. Its kno %s is not %s, so kno installs only kno",
			t.h.Label, t.target, t.before.Packages["kno"], push.KnoVersionFor(t.e.Repo, t.target))
		if push.Run(t.out, t.h.Label, t.target, push.ModeKno, t.v.Dry) != 0 {
			t.l.Warnf("%s: the kno-only push failed. Katzenpost and the services on the host are as they were", t.h.Label)
			code = 1
		}
	}
	if !t.v.Dry {
		t.last = writeRecord(t.l, t.e, t.he, t.h, t.target, t.before, t.last, runner, t.v.Push,
			!first && !t.v.Verbose, false)
	}
	if runner.verified != nil {
		t.verdict = runner.verified
	}
	t.pushErr = runner.pushErr
	return code
}

func (t *ticker) wait() {
	for readLine(t.stateFile("state")) == "pending" {
		kv, _ := readKV(t.stateFile("pending"))
		if kv["held"] == "1" {
			return
		}
		now := t.now()
		next, ok := nextCheck(kv, now)
		if !ok {
			return
		}
		t.l.Logf("%s: %s. Next check %s", t.h.Label, progress(kv), clockShort(next.Unix()))
		t.sleep(next.Sub(now))
		if t.step(false) != 0 && t.closed() {
			return
		}
	}
}

func (t *ticker) closed() bool {
	kv, _ := readKV(t.stateFile("pending"))
	deadline, err := strconv.ParseInt(kv["deadline"], 10, 64)
	return err == nil && !t.now().Before(time.Unix(deadline, 0))
}

func (t *ticker) finish() {
	if t.v.Dry || readLine(t.stateFile("state")) != "pending" || concluded(t.he.StateDir) {
		return
	}
	runner := &RemoteRunner{Env: t.he, Host: t.h, SSH: sshx.Program(),
		Log: track.Logger{Tool: "update", W: t.out}, Out: t.out}
	t.last = writeRecord(t.l, t.e, t.he, t.h, t.target, t.before, t.last, runner, t.v.Push,
		false, true)
}

func (t *ticker) outcome() (string, int) {
	kv, _ := readKV(t.stateFile("pending"))
	cur := t.he.CurrentVersion()
	switch {
	case readLine(t.stateFile("state")) == "pending" && kv["held"] == "1":
		return fmt.Sprintf("%s is held: verify cannot judge the node (%s). kno keeps it"+
			" installed and does not promote it", kv["tag"], verdictWord(kv["verdict"])), exitUnsettled
	case readLine(t.stateFile("state")) == "pending" && failedVerify(kv["verdict"]):
		return fmt.Sprintf("%s did not pass verify (%s), and kno could not roll it back."+
			" The host runs it. 'kno doctor --host %s' says what fails", kv["tag"],
			verdictWord(kv["verdict"]), t.h.Label), exitFailed
	case readLine(t.stateFile("state")) == "pending":
		return fmt.Sprintf("%s is not confirmed: kno could not reach the host, and the"+
			" verify window closed at %s", kv["tag"], clockOf(kv["deadline"])), exitUnsettled
	case cur == t.target && configsFailed(t.he.StateDir, configsOf(t.he.Repo, t.he.Network, t.h.Services)):
		return fmt.Sprintf("runs %s, and its new config packages did not pass verify. kno"+
			" installs them again only after a commit changes them. 'kno doctor --host %s'"+
			" says what fails", t.target, t.h.Label), exitFailed
	case cur == t.target && readLine(t.stateFile("last-known-good")) == t.target:
		return "runs " + t.target + ", and it is the last good release", exitDone
	case cur == t.target:
		return fmt.Sprintf("runs %s, but kno did not promote it. The last good release"+
			" is %s", t.target, orNone(readLine(t.stateFile("last-known-good")))), exitFailed
	case t.he.Quarantined(t.target):
		return fmt.Sprintf("runs %s again: %s did not pass verify, and kno quarantined it on"+
			" this host. 'kno doctor --host %s' says why; 'kno unquarantine --host %s %s'"+
			" allows it again", orNone(cur), t.target, t.h.Label, t.h.Label, t.target), exitFailed
	}
	return fmt.Sprintf("runs %s, not %s", orNone(cur), t.target), exitFailed
}

func failedVerify(verdict string) bool {
	code, err := strconv.Atoi(verdict)
	return err == nil && code != 0 && code != VerifyUnavailable && verify.AboutTheNode(code)
}

func pollEvery() time.Duration {
	if n, err := strconv.Atoi(os.Getenv("KNO_POLL_SECS")); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	switch d := promoteFloor() / 8; {
	case d < 10*time.Second:
		return 10 * time.Second
	case d > 5*time.Minute:
		return 5 * time.Minute
	default:
		return d
	}
}

func nextCheck(kv map[string]string, now time.Time) (time.Time, bool) {
	deadline, err := strconv.ParseInt(kv["deadline"], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	end := time.Unix(deadline, 0)
	if !now.Before(end) && kv["verdict"] == strconv.Itoa(VerifyUnavailable) {
		return time.Time{}, false
	}
	next := now.Add(pollEvery())
	if a, err := strconv.ParseInt(kv["activated"], 10, 64); err == nil && kv["verdict"] == "0" {
		if floor := time.Unix(a, 0).Add(promoteFloor()); now.Before(floor) && floor.Before(next) {
			next = floor.Add(time.Second)
		}
	}
	if now.Before(end) && end.Before(next) {
		next = end.Add(time.Second)
	}
	return next, true
}

func progress(kv map[string]string) string {
	parts := []string{"verifying " + kv["tag"]}
	if kv["checked"] != "" {
		parts = append(parts, "last verify "+clockShortOf(kv["checked"])+": "+verdictWord(kv["verdict"]))
	}
	if a, err := strconv.ParseInt(kv["activated"], 10, 64); err == nil {
		parts = append(parts, "promotion from "+clockShort(a+int64(promoteFloor()/time.Second)))
	}
	parts = append(parts, "window closes "+clockShortOf(kv["deadline"]))
	return strings.Join(parts, "; ")
}

func verdictWord(code string) string {
	switch code {
	case "0":
		return "passed"
	case "1":
		return "not yet"
	case "2":
		return "FAILED"
	case strconv.Itoa(verify.CodeNoConsensus):
		return "no consensus"
	case strconv.Itoa(verify.CodeCannotAsk):
		return "cannot ask"
	case strconv.Itoa(VerifyUnavailable):
		return "host did not answer"
	case "":
		return "none yet"
	}
	return "exit " + code
}

func clockShort(unix int64) string { return time.Unix(unix, 0).Local().Format("15:04") }

func clockShortOf(secs string) string {
	n, err := strconv.ParseInt(secs, 10, 64)
	if err != nil {
		return "?"
	}
	return clockShort(n)
}

func clockOf(secs string) string {
	n, err := strconv.ParseInt(secs, 10, 64)
	if err != nil {
		return "?"
	}
	return clock(n)
}

func orNone(s string) string {
	if s == "" {
		return "nothing"
	}
	return s
}

func deploying(e track.Env, hosts []remoteHost) []remoteHost {
	var out []remoteHost
	for _, h := range hosts {
		if readLine(filepath.Join(hostEnv(e, h).StateDir, "state")) == "pending" {
			continue
		}
		out = append(out, h)
	}
	return out
}

func preflight(l track.Logger, hosts []remoteHost, target string, check func(label string) error) bool {
	var bad, good []string
	for _, h := range hosts {
		if err := check(h.Label); err != nil {
			bad = append(bad, fmt.Sprintf("%s: %v", h.Label, err))
			continue
		}
		good = append(good, h.Label)
	}
	if len(bad) == 0 {
		return true
	}
	l.Errorf("%s cannot be deployed to %d of %d host(s):", target, len(bad), len(hosts))
	for _, line := range bad {
		l.Logf("  %s", line)
	}
	l.Logf("no host was contacted and nothing was changed")
	if len(good) > 0 {
		l.Logf("  the other %d host(s) have no fault. To install on them only, run:"+
			" kno update --host %s", len(good), strings.Join(good, ","))
	}
	return false
}

func selectHosts(all []remoteHost, only string) ([]remoteHost, error) {
	want := map[string]bool{}
	for _, n := range strings.FieldsFunc(only, func(r rune) bool { return r == ',' || r == ' ' }) {
		want[n] = true
	}
	var out []remoteHost
	for _, h := range all {
		if want[h.Label] {
			delete(want, h.Label)
			out = append(out, h)
		}
	}
	if len(want) > 0 {
		var missing []string
		for n := range want {
			missing = append(missing, n)
		}
		sort.Strings(missing)
		var known []string
		for _, h := range all {
			known = append(known, h.Label)
		}
		return nil, fmt.Errorf("no such host: %s (this organizer has %s)",
			strings.Join(missing, " "), strings.Join(known, " "))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--host selected nothing")
	}
	return out, nil
}

var recordMu sync.Mutex

func writeRecord(l track.Logger, e, he track.Env, h remoteHost, target string,
	before, last *record.Sample, runner *RemoteRunner, push, quiet, final bool) *record.Sample {
	recordMu.Lock()
	defer recordMu.Unlock()
	after := runner.sample("")
	if last != nil && after != nil {
		if changed := packageChanges(last.Packages, after.Packages); changed != "" {
			l.Logf("%s: now %s", h.Label, changed)
		}
	}
	rec := recordFor(e, he, h, target, before, after, thisMachine())
	wrote, err := persistRecord(e, he, rec, final)
	if err != nil {
		l.Warnf("could not write the deployment record for %s: %v", h.Label, err)
		return after
	}
	landing := final || concluded(he.StateDir)
	if wrote && (!quiet || landing) {
		l.Logf("%s: %s", h.Label, rec.Summary())
	}
	if !landing {
		if wrote && !quiet {
			l.Logf("%s: in the verify window. kno commits its deployment record when"+
				" the window closes", h.Label)
		}
		return after
	}
	landRecord(l, e, h.Label, rec, push)
	return after
}

func persistRecord(e, he track.Env, rec record.Record, final bool) (bool, error) {
	pending := record.PendingPath(he.StateDir)
	if !final && !concluded(he.StateDir) {
		return record.WriteFile(pending, rec)
	}
	wrote, err := record.Write(e.Repo, e.Network, rec)
	if err == nil {
		_ = os.Remove(pending)
	}
	return wrote, err
}

func knoDiffers(he track.Env, target string, before *record.Sample) bool {
	if before == nil || readLine(filepath.Join(he.StateDir, "state")) != "steady" ||
		he.CurrentVersion() != target {
		return false
	}
	have, want := before.Packages["kno"], push.KnoVersionFor(he.Repo, target)
	return have != "" && want != "" && have != want
}

func packageList(pkgs map[string]string) string {
	names := make([]string, 0, len(pkgs))
	for n := range pkgs {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = n + " " + pkgs[n]
	}
	return strings.Join(out, ", ")
}

func packageChanges(before, after map[string]string) string {
	names := make([]string, 0, len(after))
	for n := range after {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []string
	for _, n := range names {
		if was := before[n]; was != after[n] {
			if was == "" {
				was = "none"
			}
			out = append(out, n+" "+after[n]+" (was "+was+")")
		}
	}
	return strings.Join(out, ", ")
}

func reportNetworkConsensus(l track.Logger, verdicts map[string]int) {
	if len(verdicts) < 2 {

		return
	}
	var noConsensus, cannotAsk, in, notYet, failed, other []string
	for label, code := range verdicts {
		switch code {
		case 0:
			in = append(in, label)
		case verify.CodeNotYet:
			notYet = append(notYet, label)
		case verify.CodeFailed:
			failed = append(failed, label)
		case verify.CodeNoConsensus:
			noConsensus = append(noConsensus, label)
		case verify.CodeCannotAsk:
			cannotAsk = append(cannotAsk, label)
		default:
			other = append(other, label)
		}
	}
	for _, s := range [][]string{noConsensus, cannotAsk, in, notYet, failed, other} {
		sort.Strings(s)
	}
	if len(noConsensus) == 0 && len(cannotAsk) == 0 {
		return
	}

	total := len(verdicts)
	l.Logf("consensus across the %d host(s) asked:", total)
	for _, row := range []struct {
		name  string
		hosts []string
	}{
		{"passed (exit 0):       ", in},
		{"not yet (exit 1):      ", notYet},
		{"failed (exit 2):       ", failed},
		{"no consensus (exit 3): ", noConsensus},
		{"cannot ask (exit 4):   ", cannotAsk},
		{"verify did not run:    ", other},
	} {
		if len(row.hosts) > 0 {
			l.Logf("  %s%s", row.name, strings.Join(row.hosts, " "))
		}
	}

	switch {
	case len(noConsensus) == total:
		l.Logf("  Every host asked and none got an answer. That is the NETWORK, not")
		l.Logf("  these hosts: no package here caused it and none can fix it.")
	case len(noConsensus) > total/2:
		l.Logf("  %d of %d hosts report no consensus. The fault is probably in the", len(noConsensus), total)
		l.Logf("  network, not in these hosts.")
	case len(noConsensus) > 0 && len(in) > 0:
		l.Logf("  Consensus exists -- %s reached it -- so %s is a fault on those",
			strings.Join(in, " "), pluralHosts(len(noConsensus)))
		l.Logf("  hosts rather than an outage: start with 'kno doctor --host %s'.",
			strings.Join(noConsensus, ","))
	}
	if len(cannotAsk) > 0 {
		l.Logf("  A host that could not ask has a local fault in its client path")
		l.Logf("  (kpclientd, its socket, its config) and says nothing about the network.")
	}
}

func pluralHosts(n int) string {
	if n == 1 {
		return "that one"
	}
	return "those"
}
