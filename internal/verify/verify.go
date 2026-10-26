package verify

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/service"
	"github.com/katzenpost/kno/internal/sshx"
	"github.com/katzenpost/kno/internal/track"
)

type Runner interface {
	Run(timeout time.Duration, name string, args ...string) (string, int)
}

type Systemd interface {
	Available() bool
	State(unit string) UnitState
}

type UnitLister func() ([]string, error)

type IdentifierLister func() []NodeIdentifier

type Options struct {
	Generic       bool
	ConsensusOnly bool
	Stub          string

	Thinclient   string
	Fetch        string
	FetchTimeout time.Duration
	MinReplicas  string
	ReadyTimeout string

	CheckParticipation bool
	Identifiers        IdentifierLister

	Dataplane      bool
	Echo           bool
	EchoService    string
	Ping           string
	PingTimeout    time.Duration
	PingCount      string
	Pigeonhole     bool
	CPBench        string
	CPBenchTimeout time.Duration
	CPBenchPayload string

	MaxRestarts int
	Units       UnitLister

	Journalctl       string
	ConsensusPattern string
	Since            string

	Run     Runner
	Systemd Systemd

	Wrap []string

	FileRoot string

	Label string
}

func (o Options) FetchConsensus() (string, int) {
	if o.Fetch == "" {
		return "", 127
	}
	return o.run(o.FetchTimeout, o.Fetch, o.fetchArgs()...)
}

func (o Options) fetchArgs() []string {
	return []string{"-f", o.Thinclient, "-l", "ERROR"}
}

func (o Options) checksUnits() bool {
	return !o.Generic && !o.ConsensusOnly && o.Systemd.Available()
}

func (o Options) checksParticipation() bool {
	return o.CheckParticipation && !o.Generic && !o.ConsensusOnly
}

func (o Options) checksDataplane() bool { return o.Dataplane && !o.ConsensusOnly }

func (o Options) run(timeout time.Duration, tool string, args ...string) (string, int) {
	if len(o.Wrap) == 0 {
		return o.Run.Run(timeout, tool, args...)
	}
	return o.Run.Run(timeout, o.Wrap[0], append(append(o.Wrap[1:len(o.Wrap):len(o.Wrap)], tool), args...)...)
}

const (
	defaultConsensusPattern = "fetched.*consensus"
	defaultSince            = "-10min"
)

type Flags map[string]string

func (f Flags) get(name string) string {
	if v, ok := f[name]; ok {
		return v
	}
	return os.Getenv(name)
}

func OptionsFromEnv(args []string) (Options, error) {
	return OptionsFor(args, nil)
}

const participationEnv = "VERIFY_CHECK_PARTICIPATION"

func (f Flags) participation() string {
	if f.get(participationEnv) == "0" {
		return "0"
	}
	return "1"
}

var forwarded = []string{
	"VERIFY_CONSENSUS_ONLY", "VERIFY_GENERIC", "VERIFY_DATAPLANE", "VERIFY_PIGEONHOLE",
	"VERIFY_THINCLIENT", "VERIFY_MIN_REPLICAS", "VERIFY_READY_TIMEOUT",
	"VERIFY_FETCH_TIMEOUT", "VERIFY_MAX_RESTARTS", "VERIFY_SINCE",
	"VERIFY_ECHO", "VERIFY_ECHO_SERVICE", "VERIFY_PING_TIMEOUT", "VERIFY_PING_COUNT",
	"VERIFY_CPBENCH_TIMEOUT", "VERIFY_CPBENCH_PAYLOAD",
	"VERIFY_CONSENSUS_PATTERN", "VERIFY_NODE_IDENTIFIERS",
}

func (f Flags) remoteCommand(args []string) string {
	words := []string{participationEnv + "=" + f.participation()}
	for _, name := range forwarded {
		if v := f.get(name); v != "" {
			words = append(words, name+"="+sshx.Quote(v))
		}
	}
	words = append(words, "kno", "verify")
	if len(args) > 0 {
		words = append(words, "--")
		for _, a := range args {
			words = append(words, sshx.Quote(a))
		}
	}
	return strings.Join(words, " ")
}

func OptionsFor(args []string, f Flags) (Options, error) {
	get := f.get
	e, err := track.LoadEnv()
	if err != nil {
		return Options{}, fmt.Errorf("kno cannot read the config: %v. Correct kno.toml, or unset KNO_CONFIG.", err)
	}
	release := ""
	if len(args) > 0 {
		release = args[0]
	}

	fetchTimeout, err := secs(get, "VERIFY_FETCH_TIMEOUT", 120)
	if err != nil {
		return Options{}, err
	}
	pingTimeout, err := secs(get, "VERIFY_PING_TIMEOUT", 120)
	if err != nil {
		return Options{}, err
	}
	cpbenchTimeout, err := secs(get, "VERIFY_CPBENCH_TIMEOUT", 300)
	if err != nil {
		return Options{}, err
	}
	maxRestarts, err := strconv.Atoi(cmp.Or(get("VERIFY_MAX_RESTARTS"), strconv.Itoa(DefaultMaxRestarts)))
	if err != nil {
		return Options{}, fmt.Errorf("kno verify: bad VERIFY_MAX_RESTARTS: %v", err)
	}

	stub := ""
	if os.Getenv("KNO_TEST") == "1" {
		stub = os.Getenv("VERIFY_STUB")
	}
	explicitIDs := strings.Fields(get("VERIFY_NODE_IDENTIFIERS"))

	o := Options{
		Generic:       get("VERIFY_GENERIC") == "1",
		ConsensusOnly: get("VERIFY_CONSENSUS_ONLY") == "1",
		Stub:          stub,

		Thinclient:   cmp.Or(get("VERIFY_THINCLIENT"), defaultThinclient(runtime.GOOS == "freebsd")),
		Fetch:        FindTool(os.Getenv("KNO_FETCH"), knopath.FetchTools...),
		FetchTimeout: fetchTimeout,
		MinReplicas:  get("VERIFY_MIN_REPLICAS"),
		ReadyTimeout: cmp.Or(get("VERIFY_READY_TIMEOUT"), "2m"),

		CheckParticipation: f.participation() == "1",

		Dataplane:      get("VERIFY_DATAPLANE") == "1",
		Echo:           cmp.Or(get("VERIFY_ECHO"), "1") == "1",
		EchoService:    cmp.Or(get("VERIFY_ECHO_SERVICE"), "echo"),
		Ping:           FindTool(os.Getenv("KNO_PING"), knopath.PingTools...),
		PingTimeout:    pingTimeout,
		PingCount:      cmp.Or(get("VERIFY_PING_COUNT"), "3"),
		Pigeonhole:     get("VERIFY_PIGEONHOLE") == "1",
		CPBench:        FindTool(os.Getenv("KNO_CPBENCH"), "pq-katzenpost-cp-bench", "cp-bench"),
		CPBenchTimeout: cpbenchTimeout,
		CPBenchPayload: cmp.Or(get("VERIFY_CPBENCH_PAYLOAD"), "256"),

		MaxRestarts: maxRestarts,

		Journalctl:       lookPath("journalctl"),
		ConsensusPattern: cmp.Or(get("VERIFY_CONSENSUS_PATTERN"), defaultConsensusPattern),
		Since:            cmp.Or(get("VERIFY_SINCE"), defaultSince),

		Run:     ExecRunner{},
		Systemd: Platform(),
	}
	clientInJail(&o, e)
	if label, _, ok := config.LocalHost(e.Config); ok {
		o.Label = label
	}
	o.Units = func() ([]string, error) { return units(e, release) }
	o.Identifiers = func() []NodeIdentifier {
		if len(explicitIDs) > 0 {
			out := make([]NodeIdentifier, 0, len(explicitIDs))
			for _, id := range explicitIDs {
				out = append(out, NodeIdentifier{Where: "any", ID: id})
			}
			return out
		}
		return nodeIdentifiers(e, e.Services)
	}
	return o, nil
}

func Verify(w io.Writer, o Options) int {
	l := track.Logger{Tool: "verify", W: w}
	code := verdict(l, o)
	l.Logf("%s", Verdict(code))
	return code
}

func Verdict(code int) string {
	switch code {
	case CodeHealthy:
		return "verified (exit 0): All requested checks passed."
	case CodeNotYet:
		return "not yet (exit 1): The check is not complete. Ask again later." +
			" At the end of the verify window, kno update rolls back a host" +
			" that still gives this result."
	case CodeFailed:
		return "FAILED (exit 2): A unit does not run correctly, or a requested" +
			" test tool is missing. kno update rolls back a host with this result."
	case CodeNoConsensus:
		return "no consensus (exit 3): This host got no consensus. kno found no" +
			" local cause, so possibly the network has no consensus. kno update" +
			" keeps the new release installed and does not roll back." +
			" Compare with the other hosts."
	case CodeCannotAsk:
		return "cannot ask (exit 4): A fault on this host stops the consensus" +
			" request: kpclientd, its socket, the thin-client config or the" +
			" fetch tool. kno update keeps the new release installed and does" +
			" not roll back."
	}
	return fmt.Sprintf("exit %d: unknown result", code)
}

func verdict(l track.Logger, o Options) int {
	switch o.Stub {
	case "ok":
		return CodeHealthy
	case "notyet":
		return CodeNotYet
	case "fail":
		return CodeFailed
	}

	if o.checksUnits() {
		us, err := o.Units()
		if err != nil {
			l.Errorf("%v", err)
			return CodeNotYet
		}

		if len(us) == 0 {
			l.Errorf("kno found no service unit for this host, so it checked no unit")
			l.Logf("  The result is \"not yet\", not \"verified\".")
			l.Logf("  Run kno doctor on %s. It shows which services kno expects there.", o.host())
			return CodeNotYet
		}
		for _, u := range us {
			code, msg := ClassifyUnit(o.Systemd.State(u), o.MaxRestarts)
			if code != CodeHealthy {
				l.Logf("%s", msg)
				return code
			}
		}
	}

	consensusOK := false

	cannotAsk := false
	switch {
	case o.Fetch != "":
		args := o.fetchArgs()
		if o.MinReplicas != "" {
			args = append(args, "--require-ready", "-r", o.MinReplicas, "--ready-timeout", o.ReadyTimeout)
		}
		out, rc := o.run(o.FetchTimeout, o.Fetch, args...)
		switch {
		case rc == 0:
			if code, ok := participation(l, o, out); !ok {
				return code
			}
			consensusOK = true
		case rc == 127:
			l.Warnf("kno cannot run %s (exit 127). kno reads the kpclientd log instead.", o.Fetch)
			cannotAsk = true
		default:
			if why := cannotCheck(o, rc); why != "" {
				l.Errorf("cannot check consensus: %s", why)
				l.Errorf("This fault is on this host, not on the network.")
				showChild(l, o.Fetch, out)

				cannotAsk = true
				break
			}

			if why := crashed(o.Fetch, rc, out); why != "" {
				l.Errorf("cannot check consensus: %s", why)
				l.Errorf("This fault is on this host, not on the network.")
				showChild(l, o.Fetch, out)
				cannotAsk = true
				break
			}

			if why := clientDaemonDown(out); why != "" {
				l.Logf("cannot ask about consensus: %s", why)
				showChild(l, o.Fetch, out)
				l.Logf("  This fault is on this host, not on the network. Run these commands on %s:", o.host())
				for _, c := range clientHints(o) {
					l.Logf("      %s", c)
				}
				return CodeCannotAsk
			}

			l.Logf("no consensus (fetch exit %d): The fetch tool got no consensus."+
				" kno found no local cause.", rc)
			showChild(l, o.Fetch, out)
			return CodeNoConsensus
		}
	case o.Generic:
		l.Errorf("generic verify needs a fetch tool (set KNO_FETCH)")
		return CodeNotYet
	default:
		l.Warnf("kno cannot find kpfetch or pq-katzenpost-fetch. kno reads the kpclientd log instead.")

		cannotAsk = true
	}

	if !consensusOK && o.Journalctl != "" {
		out, _ := o.Run.Run(0, o.Journalctl, "-u", "kpclientd", "--since", o.Since)
		if matchPattern(o.ConsensusPattern, out) {
			if o.checksParticipation() {
				l.Logf("kpclientd's journal shows a consensus, but this host could not" +
					" fetch it, so participation is NOT checked")
				l.Logf("  Look for this node in the consensus from another host. If it" +
					" is there, VERIFY_CHECK_PARTICIPATION=0 accepts this consensus.")
				return CodeCannotAsk
			}
			l.Logf("kpclientd logged a consensus fetch (journalctl --since %s)."+
				" kno did not check that this node is in that consensus.", o.Since)
			consensusOK = true
		}
	}
	if !consensusOK {
		if cannotAsk {
			l.Logf("kno could not ask for a consensus, and the kpclientd log shows" +
				" no consensus fetch. Correct the fault above. Then do the check again.")
			return CodeCannotAsk
		}
		l.Logf("no consensus: kno found no consensus and no local cause.")
		return CodeNoConsensus
	}
	return dataplane(l, o)
}

func (o Options) host() string {
	if o.Label != "" {
		return o.Label
	}
	return "<label>"
}

func reinstall(label string) string {
	return "On the organizer, run: kno update --host " + label + " --force." +
		" On a host that updates itself, run: kno update --force." +
		" This installs the packages again and starts a new verify window."
}

func cannotCheck(o Options, rc int) string {
	if len(o.Wrap) > 0 && o.FileRoot == "" {
		return fmt.Sprintf("the jail %s for kpclientd does not exist on %s. Create the jail."+
			" Then install kpclientd in it. %s", o.Wrap[len(o.Wrap)-1], o.host(), reinstall(o.host()))
	}
	p := filepath.Join(o.FileRoot, o.Thinclient)
	if why := ThinclientProblem(p); why != "" {
		if _, err := os.Stat(p); o.Thinclient != "" && os.IsNotExist(err) {
			why += " " + reinstall(o.host())
		}
		return why
	}
	switch rc {
	case 124:
		return fmt.Sprintf("%s did not answer within %s", o.Fetch, o.FetchTimeout)
	case 126:
		return fmt.Sprintf("%s is present but will not execute"+
			" (not executable, or built for another architecture)", o.Fetch)
	}
	return ""
}

func crashed(tool string, rc int, out string) string {
	name := filepath.Base(tool)
	if rc < 0 {
		return fmt.Sprintf("%s was stopped by a signal", name)
	}
	if rc != 2 {
		return ""
	}
	for _, sig := range []string{"panic:", "fatal error:", "SIGILL", "SIGSEGV", "SIGBUS"} {
		if strings.Contains(out, sig) {
			return fmt.Sprintf("%s crashed (rc=2, %s)", name, strings.TrimSuffix(sig, ":"))
		}
	}
	return ""
}

func ThinclientProblem(path string) string {
	if path == "" {
		return "no thin-client config is set (VERIFY_THINCLIENT)"
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("%s does not exist. A package that kno installs"+
				" contains this file.", path)
		}
		return fmt.Sprintf("%s cannot be read (%v)", path, err)
	}
	f.Close()
	return ""
}

func showChild(l track.Logger, tool, out string) {
	name := filepath.Base(tool)
	n := 0
	for _, ln := range strings.Split(out, "\n") {
		if ln = strings.TrimRight(ln, "\r"); strings.TrimSpace(ln) == "" {
			continue
		}
		if n++; n > 6 {
			l.Logf("  %s: (further output suppressed)", name)
			return
		}
		l.Logf("  %s: %s", name, ln)
	}
	if n == 0 {
		l.Logf("  %s: said nothing at all", name)
	}
}

func participation(l track.Logger, o Options, dump string) (int, bool) {
	if !o.checksParticipation() {
		l.Logf("consensus present")
		return CodeHealthy, true
	}
	ids := o.Identifiers()
	if len(ids) == 0 {
		l.Errorf("kno cannot check participation. No config of this host has an Identifier line.")
		return CodeNotYet, false
	}
	if bad, ok := Participating(dump, ids); !ok {
		l.Logf("The consensus does not list node '%s' yet.", bad.ID)
		return CodeNotYet, false
	}
	l.Logf("consensus present, node participating")
	return CodeHealthy, true
}

func dataplane(l track.Logger, o Options) int {
	if !o.checksDataplane() {
		return CodeHealthy
	}
	if o.Echo {
		if o.Ping == "" {
			l.Logf("data-plane: kno cannot find the ping tool (kpping). Install it on %s, or set KNO_PING.", o.host())
			return CodeFailed
		}
		if _, rc := o.run(o.PingTimeout, o.Ping,
			"-c", o.Thinclient, "-s", o.EchoService, "--thin", "-n", o.PingCount); rc != 0 {
			l.Logf("data-plane: echo ping failed")
			return CodeNotYet
		}
		l.Logf("data-plane: echo ping ok")
	}
	if o.Pigeonhole {
		if o.CPBench == "" {
			l.Logf("data-plane: kno cannot find the pigeonhole test tool (cp-bench)."+
				" Install it on %s, or set KNO_CPBENCH.", o.host())
			return CodeFailed
		}
		if _, rc := o.run(o.CPBenchTimeout, o.CPBench,
			"-config", o.Thinclient, "-payload", o.CPBenchPayload,
			"-metrics", "", "-log-level", "ERROR"); rc != 0 {
			l.Logf("data-plane: pigeonhole round-trip failed")
			return CodeNotYet
		}
		l.Logf("data-plane: pigeonhole round-trip ok")
	}
	return CodeHealthy
}

func matchPattern(pattern, out string) bool {
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return false
	}
	return re.MatchString(out)
}

func units(e track.Env, release string) ([]string, error) {
	if release != "" && fsutil.IsDir(filepath.Join(release, "manifest")) {
		return manifestUnits(filepath.Join(release, "manifest")), nil
	}
	if len(e.Services) == 0 {
		return nil, errors.New("This machine runs no services. To check a host," +
			" run kno verify --host <label>. To make this machine a node, run kno" +
			" setup or install kno-host-config.")
	}
	var out []string
	for _, s := range e.Services {
		if !e.HasService(s) {
			continue
		}
		sv, err := service.ResolveService(e.Repo, e.Network, s)
		if err != nil || sv.Unit == "" {
			continue
		}
		out = append(out, ServiceUnit(sv))
	}
	return out, nil
}

func manifestUnits(dir string) []string {
	files, _ := filepath.Glob(filepath.Join(dir, "*.conf"))
	var out []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var sv service.Service
		for _, ln := range strings.Split(string(b), "\n") {
			k, v, _ := strings.Cut(strings.TrimSpace(ln), "=")
			v = strings.Trim(v, `"'`)
			switch k {
			case "UNIT":
				sv.Unit = v
			case "JAIL":
				sv.Jail = v
			case "BUILD":
				sv.Build = v
			}
		}
		if sv.Unit != "" {
			out = append(out, ServiceUnit(sv))
		}
	}
	return out
}

var identLine = regexp.MustCompile(`^[[:space:]]*Identifier[[:space:]]*=`)

func nodeIdentifiers(e track.Env, services []string) []NodeIdentifier {
	seen := map[string]bool{}
	var keys []string
	for _, s := range services {
		id := identifierOf(e, s)
		if id == "" {
			continue
		}
		where := "node"
		if strings.HasSuffix(s, "-pq-authority") {
			where = "authority"
		}
		k := where + "|" + id
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := make([]NodeIdentifier, 0, len(keys))
	for _, k := range keys {
		where, id, _ := strings.Cut(k, "|")
		out = append(out, NodeIdentifier{Where: where, ID: id})
	}
	return out
}

func identifierOf(e track.Env, svc string) string {
	cands := []string{
		filepath.Join(knopath.ConfigsDir(e.Repo, e.Network), svc+".toml"),
		filepath.Join(e.Repo, "configs", svc+".toml"),
	}
	for _, p := range cands {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, ln := range strings.Split(string(b), "\n") {
			if !identLine.MatchString(ln) {
				continue
			}
			i := strings.LastIndex(ln, "=")
			return strings.ReplaceAll(strings.TrimLeft(ln[i+1:], " "), `"`, "")
		}
	}
	return ""
}

func FindTool(explicit string, candidates ...string) string {
	if explicit != "" {
		candidates = []string{explicit}
	}
	for _, c := range candidates {
		if lookPath(c) != "" {
			return c
		}
	}
	return ""
}

func lookPath(p string) string {
	found, err := exec.LookPath(p)
	if err != nil {
		return ""
	}
	return found
}

func secs(get func(string) string, name string, def int) (time.Duration, error) {
	v := get(name)
	if v == "" {
		return time.Duration(def) * time.Second, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("kno verify: bad %s: %v", name, err)
	}
	return time.Duration(n) * time.Second, nil
}

type ExecRunner struct{}

func (ExecRunner) Run(timeout time.Duration, name string, args ...string) (string, int) {
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, name, args...)

	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	err := cmd.Run()
	fail := func(rc int) (string, int) {
		return strings.TrimRight(out.String()+errs.String(), "\n"), rc
	}
	if ctx.Err() == context.DeadlineExceeded {
		return fail(124)
	}
	if err == nil {
		return out.String(), 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return fail(ee.ExitCode())
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return fail(127)
	}
	return fail(126)
}

type Systemctl struct{}

func (Systemctl) Available() bool { return lookPath("systemctl") != "" }

func (Systemctl) State(unit string) UnitState {
	u := UnitState{Unit: unit, Restarts: -1}
	u.Failed = exec.Command("systemctl", "is-failed", "--quiet", unit).Run() == nil
	out, _ := exec.Command("systemctl", "is-active", unit).Output()
	u.Active = strings.TrimRight(string(out), "\n")

	en, _ := exec.Command("systemctl", "is-enabled", unit).Output()
	u.Enabled = strings.TrimRight(string(en), "\n") == "enabled"
	out, _ = exec.Command("systemctl", "show", "-p", "NRestarts", "--value", unit).Output()
	if n, err := strconv.Atoi(strings.TrimRight(string(out), "\n")); err == nil && n >= 0 {
		u.Restarts = n
	}
	return u
}

func clientDaemonDown(out string) string {
	l := strings.ToLower(out)
	switch {
	case strings.Contains(l, "connect: connection refused"),
		strings.Contains(l, "failed to connect to client daemon"):
		return "the local client daemon (kpclientd) is not running"
	case strings.Contains(l, "no such file or directory") && strings.Contains(l, "katzenpost"):
		return "the local client daemon (kpclientd) is not listening on the socket this host dials"
	}
	return ""
}
