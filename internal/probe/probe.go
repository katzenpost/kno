package probe

import (
	"cmp"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/track"
	"github.com/katzenpost/kno/internal/verify"
)

const (
	CodeDiscrepancy = 1
	CodeSettling    = 3
)

type Dialer func(addr string, timeout time.Duration) bool

type Fetcher func() string

type Options struct {
	ConfigsDir   string
	Thinclient   string
	Timeout      time.Duration
	KnownOrphans []string
	Dial         Dialer
	Fetch        Fetcher
}

const (
	defaultTimeoutSecs      = 5
	defaultFetchTimeoutSecs = 120
)

func OptionsFromEnv(configs, thin, timeout, fetch string) (Options, error) {
	configs = cmp.Or(configs, os.Getenv("PROBE_CONFIGS"))
	thin = cmp.Or(thin, os.Getenv("VERIFY_THINCLIENT"))
	timeout = cmp.Or(timeout, os.Getenv("PROBE_TIMEOUT"))
	fetch = cmp.Or(fetch, os.Getenv("KNO_FETCH"))

	t, err := secs(timeout, defaultTimeoutSecs)
	if err != nil {
		return Options{}, fmt.Errorf("kno probe: bad timeout: %v", err)
	}
	ft, err := secs(os.Getenv("PROBE_FETCH_TIMEOUT"), defaultFetchTimeoutSecs)
	if err != nil {
		return Options{}, fmt.Errorf("kno probe: bad fetch timeout: %v", err)
	}
	if configs == "" {
		e, err := track.LoadEnv()
		if err != nil {
			return Options{}, fmt.Errorf("kno cannot read the config: %v. Correct kno.toml, or unset KNO_CONFIG.", err)
		}
		configs = defaultConfigsDir(e)
	}

	if fetch == "" {
		fetch = verify.FindTool("", knopath.FetchTools...)
	}
	return Options{
		ConfigsDir:   configs,
		Thinclient:   thin,
		Timeout:      t,
		KnownOrphans: KnownOrphans(os.Getenv("PROBE_KNOWN_ORPHANS")),
		Dial:         TCPDial,
		Fetch:        ExecFetcher(fetch, thin, ft),
	}, nil
}

func defaultConfigsDir(e track.Env) string {
	if e.Repo != "" {
		if e.Network != "" {
			if d := knopath.ConfigsDir(e.Repo, e.Network); fsutil.IsDir(d) {
				return d
			}
		}
		if d := filepath.Join(e.Repo, "configs"); fsutil.IsDir(d) {
			return d
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return "configs"
	}
	return filepath.Join(filepath.Dir(exe), "..", "configs")
}

func secs(v string, def int) (time.Duration, error) {
	if v == "" {
		return time.Duration(def) * time.Second, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, err
	}
	return time.Duration(n) * time.Second, nil
}

func TCPDial(addr string, timeout time.Duration) bool {
	c, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func ExecFetcher(fetch, thin string, timeout time.Duration) Fetcher {
	return func() string {
		if fetch == "" || thin == "" {
			return ""
		}
		doc, _ := verify.ExecRunner{}.Run(timeout, fetch, "-f", thin, "--format", "json")
		dump, _ := verify.ExecRunner{}.Run(timeout, fetch, "-f", thin)
		return doc + DumpMark + dump
	}
}

func Probe(w io.Writer, o Options) int {
	l := track.Logger{Tool: "probe", W: w}
	eps, err := ScanConfigs(o.ConfigsDir)
	if err != nil {
		l.Logf("configs dir not found: %s", o.ConfigsDir)
		return 2
	}

	cons, consOK := ParseConsensus(o.Fetch())
	if !consOK {
		l.Warnf("no consensus document (fetch/thinclient unset or network down). Reachability-only")
	}

	fmt.Fprintf(w, "%-9s %-18s %-22s %s\n", "ROLE", "IDENTIFIER", "ADDRESS", "STATUS")
	issues := 0
	hard := 0
	if o.Thinclient != "" && !consOK {

		l.Logf("consensus requested (thinclient set) but not fetched")
		issues++
		hard++
	}

	seen := map[string]bool{}
	for _, ep := range eps {
		key := ep.Role + "|" + ep.Identifier
		if seen[key] {
			continue
		}
		seen[key] = true

		v := Classify(Observation{
			Role:          ep.Role,
			Up:            reachableAny(o, eps, ep.Role, ep.Identifier),
			ConsensusOK:   consOK,
			ConfigAddr:    ep.Addr(),
			ConsensusAddr: cons.Addr(ep.Identifier),
			Signing:       cons.Signed(ep.Identifier),
			KnownOrphan:   IsKnownOrphan(o.KnownOrphans, ep.Identifier),
		})
		if v.Discrepancy {
			issues++
			if v.Status != StatusOrphan {
				hard++
			}
		}
		fmt.Fprintf(w, "%-9s %-18s %-22s %s\n", ep.Role, ep.Identifier, ep.Addr(), v.Status)
	}

	if consOK {
		for _, n := range UnknownInConsensus(cons.Nodes, eps) {
			fmt.Fprintf(w, "%-9s %-18s %-22s %s\n", "?", n.Identifier, n.Addr(), StatusUnknown)
			issues++
			hard++
		}
	}

	if issues > 0 {

		if hard == 0 {
			l.Logf("probe: %d not in consensus yet (reachable; consensus settles on an epoch boundary)", issues)
			return CodeSettling
		}
		l.Logf("probe: %d discrepancy(ies), %d of them faults", issues, hard)
		return CodeDiscrepancy
	}
	l.Logf("probe: all servers reachable and consistent with consensus")
	return 0
}

func reachableAny(o Options, eps []Endpoint, role, id string) bool {
	for _, e := range eps {
		if e.Role == role && e.Identifier == id && o.Dial(e.Addr(), o.Timeout) {
			return true
		}
	}
	return false
}

func ScanConfigs(dir string) ([]Endpoint, error) {
	fi, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*-pq-*.toml"))
	if err != nil {
		return nil, err
	}
	var out []Endpoint
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		out = append(out, ParseConfig(strings.TrimSuffix(filepath.Base(f), ".toml"), b)...)
	}
	return out, nil
}

var (
	addrLine = regexp.MustCompile(`^[[:space:]]*Addresses`)
	tcpAddr  = regexp.MustCompile(`tcp://([0-9]+\.[0-9]+\.[0-9]+\.[0-9]+):([0-9]+)`)
)

func ParseConfig(base string, data []byte) []Endpoint {
	lines := strings.Split(string(data), "\n")
	id := ""
	for _, ln := range lines {
		if strings.Contains(ln, "Identifier") {
			id = valueOf(ln)
			break
		}
	}
	if id == "" {
		return nil
	}
	role := RoleOf(base)
	for _, ln := range lines {
		if !addrLine.MatchString(ln) {
			continue
		}
		var out []Endpoint
		for _, m := range tcpAddr.FindAllStringSubmatch(ln, -1) {
			out = append(out, Endpoint{Role: role, Identifier: id, Host: m[1], Port: m[2]})
		}
		return out
	}
	return nil
}

func valueOf(ln string) string {
	i := strings.LastIndex(ln, "=")
	if i < 0 {
		return strings.ReplaceAll(ln, `"`, "")
	}
	return strings.ReplaceAll(strings.TrimLeft(ln[i+1:], " "), `"`, "")
}

var (
	consNode = regexp.MustCompile(`\{([a-z0-9]+) [0-9a-f]{8,} map\[tcp[46]?:\[tcp://([0-9.]+):([0-9]+)`)
)

const DumpMark = "\n#kno-dump\n"

func ParseConsensus(out string) (Consensus, bool) {
	docText, dump, _ := strings.Cut(out, DumpMark)
	d, ok := verify.ParseDoc(docText)
	if !ok {
		return Consensus{}, false
	}
	named := map[string]bool{}
	for _, n := range d.Nodes() {
		named[n] = true
	}
	var c Consensus
	seen := map[string]bool{}
	for _, m := range consNode.FindAllStringSubmatch(dump, -1) {
		if named[m[1]] && !seen[m[1]] {
			seen[m[1]] = true
			c.Nodes = append(c.Nodes, ConsensusNode{Identifier: m[1], Host: m[2], Port: m[3]})
		}
	}
	sort.Slice(c.Nodes, func(i, j int) bool { return c.Nodes[i].Identifier < c.Nodes[j].Identifier })
	c.Signers = append([]string{}, d.Signers...)
	sort.Strings(c.Signers)
	return c, true
}
