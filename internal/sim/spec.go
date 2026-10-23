package sim

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/nodefile"
	"github.com/katzenpost/kno/internal/service"
)

type daemon struct {
	Ctr     string
	IP      string
	Base    string
	Bin     string
	DataSrc string
	Arch    string
	OS      string
}

type platform struct {
	Goarch string
	OS     string
}

func (p platform) String() string { return p.OS + "/" + p.Goarch }

func (p platform) debArch() string {
	if p.Goarch == "ppc64le" {
		return "ppc64el"
	}
	return p.Goarch
}

func nodeOf(base string) string {
	n, _ := nodefile.NodeOf(base)
	return n
}

func rolePrefixBin(base string) (prefix, role string) {
	switch {
	case strings.HasSuffix(base, "-pq-authority"):
		return "authority", roleAuthority
	case strings.HasSuffix(base, "-pq-mixserver"):
		return "mix", roleServer
	case strings.HasSuffix(base, "-pq-gatewaynode"):
		return "gateway", roleServer
	case strings.HasSuffix(base, "-pq-servicenode"):
		return "service", roleServer
	case strings.HasSuffix(base, "-pq-storagenode") || strings.Contains(base, "-pq-storagenode-"):
		return "replica", roleReplica
	}
	return "", ""
}

func (r *run) binOf(base, role string) (string, error) {
	pre := ""
	if s, err := service.ResolveService(r.Repo, r.Network, base); err == nil {
		pre = s.BinaryDst
	}
	ix, err := r.index()
	if err != nil {
		if pre != "" {
			return pre, nil
		}
		return "", err
	}
	return ix.binFor(role, pre)
}

func (r *run) platformOf(d daemon) platform {
	goarch, _, err := compile.ArchToGo(d.Arch)
	if err != nil || goarch == "" {
		return r.clientPlatform()
	}
	if !r.Emulate && goarch != hostGoarch {
		return r.clientPlatform()
	}
	return platform{Goarch: goarch, OS: d.OS}
}

func (r *run) clientPlatform() platform {
	return platform{Goarch: hostGoarch, OS: config.NodeOS(r.Repo, r.Network, "")}
}

var (
	reIdent    = regexp.MustCompile(`^.*= *"(.*)".*$`)
	reAddrLine = regexp.MustCompile(`(?i)^[[:space:]]*Addresses[[:space:]]*=`)
	reBindLine = regexp.MustCompile(`(?i)^[[:space:]]*BindAddresses[[:space:]]*=`)
	rePeers    = regexp.MustCompile(`^[[:space:]]*\[\[`)
	reTCPAddr  = regexp.MustCompile(`tcp://([0-9]+\.[0-9]+\.[0-9]+\.[0-9]+:[0-9]+)`)
	reIPPort   = regexp.MustCompile(`[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+:[0-9]+`)
	reIndent   = regexp.MustCompile(`^[[:space:]]*`)
)

func cfgIdent(lines []string) string {
	for _, l := range lines {
		if strings.Contains(l, "Identifier") {
			if m := reIdent.FindStringSubmatch(l); m != nil {
				return m[1]
			}
			return ""
		}
	}
	return ""
}

func cfgIP(lines []string) string {
	for _, l := range lines {
		if !reAddrLine.MatchString(l) {
			continue
		}
		if m := reTCPAddr.FindStringSubmatch(l); m != nil {
			return strings.SplitN(m[1], ":", 2)[0]
		}
		return ""
	}
	return ""
}

func (r *run) ctrForIP(ip string) string {
	return r.Net + "_c_" + strings.ReplaceAll(ip, ".", "_")
}

func (r *run) deriveSpec() ([]daemon, error) {
	cfgs, err := filepath.Glob(filepath.Join(r.cfg, "*-pq-*.toml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(cfgs)
	var out []daemon
	ctrs := map[string]bool{}
	for _, cfg := range cfgs {
		base := strings.TrimSuffix(filepath.Base(cfg), ".toml")
		prefix, role := rolePrefixBin(base)
		if prefix == "" {
			return nil, fmt.Errorf("cannot derive role for %s", base)
		}
		bin, err := r.binOf(base, role)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", base, err)
		}
		lines, err := readLines(cfg)
		if err != nil {
			return nil, err
		}
		ident, ip := cfgIdent(lines), cfgIP(lines)
		if ident == "" {
			return nil, fmt.Errorf("no Identifier in %s", base)
		}
		if ip == "" {
			return nil, fmt.Errorf("no IPv4 address in %s", base)
		}
		node := nodeOf(base)
		ctr := r.ctrForIP(ip)
		ctrs[ctr] = true
		out = append(out, daemon{Ctr: ctr, IP: ip, Base: base, Bin: bin,
			DataSrc: prefix + "-" + ident,
			Arch:    config.NodeArch(r.Repo, r.Network, node),
			OS:      r.containerOS(node)})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no *-pq-*.toml configs in %s (run 'configs' first)", r.cfg)
	}
	lines := make([]string, 0, len(out))
	for _, s := range out {
		lines = append(lines, strings.Join([]string{s.Ctr, s.IP, s.Base, s.Bin, s.DataSrc,
			s.Arch, s.OS, r.platformOf(s).String()}, " "))
	}
	if err := writeLines(r.spec, lines, 0o644); err != nil {
		return nil, err
	}
	r.log.Logf("derived spec: %d services on %d host containers", len(out), len(ctrs))
	r.reportForeignArches(out)
	return out, nil
}

func (r *run) reportForeignArches(svcs []daemon) {
	if r.Emulate {
		return
	}
	foreign := map[string][]string{}
	for _, s := range svcs {
		goarch, _, err := compile.ArchToGo(s.Arch)
		if err != nil || goarch == hostGoarch {
			continue
		}
		foreign[s.Arch] = append(foreign[s.Arch], s.Base)
	}
	arches := make([]string, 0, len(foreign))
	for a := range foreign {
		arches = append(arches, a)
	}
	sort.Strings(arches)
	for _, a := range arches {
		sort.Strings(foreign[a])
		r.log.Warnf("%d service(s) are %s and run here as %s: %s",
			len(foreign[a]), a, hostGoarch, strings.Join(foreign[a], " "))
	}
	if len(arches) > 0 {
		r.log.Logf("  this run does not test their architecture; --emulate does")
	}
}

var (
	reV6Addr   = regexp.MustCompile(`"tcp://\[[^\]]*\][^"]*",?`)
	reOnion    = regexp.MustCompile(`"onion://[^"]*",?`)
	reTrailSep = regexp.MustCompile(`,[[:space:]]*\]`)
	reLeadSep  = regexp.MustCompile(`\[[[:space:]]*,`)
)

func stripV6(l string) string {
	l = reV6Addr.ReplaceAllString(l, "")
	l = reOnion.ReplaceAllString(l, "")
	l = reTrailSep.ReplaceAllString(l, "]")
	return reLeadSep.ReplaceAllString(l, "[")
}

func indentOf(l string) string { return reIndent.FindString(l) }

func keyLine(l, key string) bool {
	t := strings.TrimLeft(l, " \t")
	if len(t) < len(key) || !strings.EqualFold(t[:len(key)], key) {
		return false
	}
	rest := strings.TrimLeft(t[len(key):], " \t")
	return strings.HasPrefix(rest, "=")
}

func transformServerConfig(lines []string, bind, ddir, level string) []string {
	out := make([]string, 0, len(lines))
	inPeers := false
	for _, l := range lines {
		if rePeers.MatchString(l) {
			inPeers = true
		}
		ind := indentOf(l)
		switch {
		case keyLine(l, "MetricsAddress"):
			l = ind + `MetricsAddress = ""`
		case keyLine(l, "BindAddresses") && !inPeers:
			l = ind + "BindAddresses = " + bind
		case keyLine(l, "DataDir"):
			l = ind + `DataDir = "` + ddir + `"`
		case keyLine(l, "Level"):
			l = ind + `Level = "` + level + `"`
		case keyLine(l, "SpoolDB"):
			l = ind + `SpoolDB = "` + ddir + `/spool.db"`
		}
		out = append(out, stripV6(l))
	}
	return out
}

func transformCourierConfig(lines []string, level string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		ind := indentOf(l)
		switch {
		case keyLine(l, "DataDir"):
			l = ind + `DataDir = "/courierdata"`
		case keyLine(l, "File"):
			l = ind + `File = "/courierdata/courier.log"`
		case keyLine(l, "MetricsAddress"):
			l = ind + `MetricsAddress = ""`
		case keyLine(l, "BindAddresses"):
			continue
		case keyLine(l, "Level"):
			l = ind + `Level = "` + level + `"`
		}
		out = append(out, stripV6(l))
	}
	return out
}

func disableCourierBlock(lines []string) []string {
	out := make([]string, 0, len(lines))
	inBlock := false
	for _, l := range lines {
		switch {
		case strings.Contains(l, "[[ServiceNode.CBORPluginKaetzchen]]"):
			inBlock = true
		case inBlock && keyLine(l, "Disable"):
			l = l[:strings.Index(l, "=")] + "= true"
			inBlock = false
		}
		out = append(out, l)
	}
	return out
}

func addrsOn(lines []string, re *regexp.Regexp) []string {
	for _, l := range lines {
		if re.MatchString(l) {
			return reIPPort.FindAllString(l, -1)
		}
	}
	return nil
}

func ownBlock(lines []string) []string {
	for i, l := range lines {
		if rePeers.MatchString(l) {
			return lines[:i+1]
		}
	}
	return lines
}

func portOf(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[i+1:]
	}
	return addr
}

type natRule struct{ AnnIP, AnnPort, BindIP, BindPort string }

func bindFor(ann, bnd []string, shared bool) (bind string, nat []natRule) {
	if shared {
		bnd = nil
	}
	var parts []string
	if len(bnd) > 0 {
		for _, b := range bnd {
			parts = append(parts, `"tcp://`+b+`"`)
		}
		for i, a := range ann {
			j := i
			if j >= len(bnd) {
				j = len(bnd) - 1
			}
			ai := strings.SplitN(a, ":", 2)
			bi := strings.SplitN(bnd[j], ":", 2)
			nat = append(nat, natRule{AnnIP: ai[0], AnnPort: ai[1], BindIP: bi[0], BindPort: bi[1]})
		}
	} else {
		for _, a := range ann {
			parts = append(parts, `"tcp://0.0.0.0:`+portOf(a)+`"`)
		}
	}
	return "[" + strings.Join(parts, ",") + "]", nat
}

var authorityKeyKinds = []string{"mix-id-keys", "gateway-id-keys", "servicenode-id-keys", "replica-id-keys"}

func (r *run) prepConfigs(svcs []daemon) error {
	for _, d := range []string{r.sim, r.data} {
		if err := os.RemoveAll(d); err != nil {
			return err
		}
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	count := map[string]int{}
	for _, s := range svcs {
		count[s.Ctr]++
	}
	for _, s := range svcs {
		if err := r.prepOne(s, count[s.Ctr] > 1); err != nil {
			return err
		}
	}
	if err := copyFile(filepath.Join(r.cfg, "thinclient.toml"), filepath.Join(r.sim, "thinclient.toml"), 0o644); err != nil {
		return err
	}
	client, err := readLines(filepath.Join(r.cfg, "client.toml"))
	if err != nil {
		return err
	}
	for i, l := range client {
		client[i] = stripV6(l)
	}
	if err := writeLines(filepath.Join(r.sim, "client.toml"), client, 0o644); err != nil {
		return err
	}
	if err := r.prepCouriers(); err != nil {
		return err
	}
	n, _ := filepath.Glob(filepath.Join(r.sim, "*.toml"))
	r.log.Logf("prepared %d sim configs", len(n))
	return nil
}

func (r *run) prepOne(s daemon, shared bool) error {
	lines, err := readLines(filepath.Join(r.cfg, s.Base+".toml"))
	if err != nil {
		return err
	}
	src := filepath.Join(r.dd, s.DataSrc)
	if !fsutil.IsDir(src) {
		return fmt.Errorf("missing datadir %s", src)
	}
	own := ownBlock(lines)
	ann := addrsOn(own, reAddrLine)
	if len(ann) == 0 {
		return fmt.Errorf("no IPv4 address in %s", s.Base)
	}
	bind, nat := bindFor(ann, addrsOn(own, reBindLine), shared)
	natLines := make([]string, 0, len(nat))
	for _, n := range nat {
		natLines = append(natLines, strings.Join([]string{n.AnnIP, n.AnnPort, n.BindIP, n.BindPort}, " "))
	}
	if err := writeLines(filepath.Join(r.sim, s.Base+".nat"), natLines, 0o644); err != nil {
		return err
	}
	state := filepath.Join(r.data, s.Base)
	if err := copyTree(src, state); err != nil {
		return err
	}
	if prefix, _ := rolePrefixBin(s.Base); prefix == "authority" {
		for _, kd := range authorityKeyKinds {
			if err := copyGlob(filepath.Join(r.keys, kd, "*.pem"), state); err != nil {
				return err
			}
		}
	}
	if err := os.Chmod(state, 0o700); err != nil {
		return err
	}
	out := transformServerConfig(lines, bind, "/data/"+s.Base, r.LogLevel)
	if r.NoCourier && containsAny(out, "CBORPluginKaetzchen") {
		out = disableCourierBlock(out)
	}
	return writeLines(filepath.Join(r.sim, s.Base+".toml"), out, 0o644)
}

func (r *run) prepCouriers() error {
	courierCfg := filepath.Join(r.cfg, "courier.toml")
	if _, err := os.Stat(courierCfg); err != nil {
		return nil
	}
	lines, err := readLines(courierCfg)
	if err != nil {
		return err
	}
	if err := writeLines(filepath.Join(r.sim, "courier.toml"),
		transformCourierConfig(lines, r.LogLevel), 0o644); err != nil {
		return err
	}
	dirs, err := filepath.Glob(filepath.Join(r.dd, "courier-*"))
	if err != nil {
		return err
	}
	for _, d := range dirs {
		state := filepath.Join(r.data, filepath.Base(d))
		if err := copyTree(d, state); err != nil {
			return err
		}
		if err := os.Chmod(state, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func containsAny(lines []string, needle string) bool {
	for _, l := range lines {
		if strings.Contains(l, needle) {
			return true
		}
	}
	return false
}
