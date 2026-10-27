package config

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/katzenpost/kno/internal/fsutil"

	"github.com/BurntSushi/toml"

	knorepo "github.com/katzenpost/kno/data"
	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/nodefile"
)

type Host struct {
	SSH       string   `toml:"ssh,omitempty"`
	Hostname  string   `toml:"hostname,omitempty"`
	Scheduler string   `toml:"scheduler,omitempty"`
	Services  []string `toml:"services,omitempty"`

	Kpclientd *bool `toml:"kpclientd,omitempty"`

	Install string `toml:"install,omitempty"`

	KpclientdUser string `toml:"kpclientd_user,omitempty"`

	Privilege string `toml:"privilege,omitempty"`

	ClientJail string `toml:"client_jail,omitempty"`
}

const DefaultClientJail = "kpclientd"

func (h Host) KpclientdJail() string {
	if h.ClientJail != "" {
		return h.ClientJail
	}
	return DefaultClientJail
}

func LocalClientJail(c Config) string {
	_, h, _ := LocalHost(c)
	return h.KpclientdJail()
}

const (
	PrivNone = "none"

	PrivSudo = "sudo"

	PrivKno = "kno"

	PrivApt = "apt"
)

var Privileges = []string{PrivNone, PrivSudo, PrivKno, PrivApt}

var PrivilegeHelp = []string{
	"none  kno prints the commands; you run them  (default)",
	"sudo  kno runs them with YOUR sudo rights; no new policy",
	"kno   kno runs them unattended, with the sudo rights you give its account",
	"apt   this host's apt policy installs; kno only publishes",
}

func (h Host) PrivilegeMode() string { return privilegeOr(h.Privilege, PrivNone) }

func privilegeOr(v, fallback string) string {
	if cur, ok := NormalizePrivilege(v); ok {
		return cur
	}
	return fallback
}

func (c Config) Privilege(label string) string {
	if h, ok := c.Hosts[label]; ok && h.Privilege != "" {
		return privilegeOr(h.Privilege, c.defaultPrivilege())
	}
	return c.defaultPrivilege()
}

func (c Config) defaultPrivilege() string {
	return privilegeOr(c.Defaults.Privilege, PrivNone)
}

func (c Config) Scheduler(label string) string {
	if h, ok := c.Hosts[label]; ok && h.Scheduler != "" {
		return h.Scheduler
	}
	if c.Defaults.Scheduler != "" {
		return c.Defaults.Scheduler
	}
	return "none"
}

func (c Config) RunsKpclientd(label string) bool {
	if h, ok := c.Hosts[label]; ok && h.Kpclientd != nil {
		return *h.Kpclientd
	}
	if c.Defaults.Kpclientd != nil {
		return *c.Defaults.Kpclientd
	}
	return true
}

func (c Config) KpclientdUser(label string) string {
	if h, ok := c.Hosts[label]; ok && h.KpclientdUser != "" {
		return h.KpclientdUser
	}
	return c.Defaults.KpclientdUser
}

func (c Config) Exceptions() map[string][]string {
	out := map[string][]string{}
	for label, h := range c.Hosts {
		var diff []string
		if h.Privilege != "" && c.Privilege(label) != c.defaultPrivilege() {
			diff = append(diff, "privilege="+c.Privilege(label))
		}
		if h.Scheduler != "" && h.Scheduler != c.Scheduler("") {
			diff = append(diff, "scheduler="+h.Scheduler)
		}
		if h.Kpclientd != nil && *h.Kpclientd != c.RunsKpclientd("") {
			diff = append(diff, fmt.Sprintf("kpclientd=%v", *h.Kpclientd))
		}
		if h.KpclientdUser != "" && h.KpclientdUser != c.Defaults.KpclientdUser {
			diff = append(diff, "kpclientd_user="+h.KpclientdUser)
		}
		if len(diff) > 0 {
			sort.Strings(diff)
			out[label] = diff
		}
	}
	return out
}

func ValidPrivilege(v string) bool {
	if v == "" {
		return true
	}
	_, ok := NormalizePrivilege(v)
	return ok
}

var legacyPrivilege = map[string]string{
	"manual":   PrivNone,
	"operator": PrivSudo,
}

func NormalizePrivilege(v string) (string, bool) {
	for _, p := range Privileges {
		if v == p {
			return p, true
		}
	}
	if cur, ok := legacyPrivilege[v]; ok {
		return cur, true
	}
	return "", false
}

func (h Host) RunsKpclientd() bool { return h.Kpclientd == nil || *h.Kpclientd }

func Bool(v bool) *bool { return &v }

type Config struct {
	Network string   `toml:"network"`
	Nodes   []string `toml:"nodes"`
	Update  struct {
		Policy           string `toml:"policy"`
		RequireSignedTag bool   `toml:"require_signed_tag"`

		RequireSignatures bool `toml:"require_signatures,omitempty"`

		Threshold int `toml:"threshold,omitempty"`
	} `toml:"update"`
	Push struct {
		Mode string `toml:"mode"`
	} `toml:"push"`

	Defaults HostDefaults      `toml:"defaults"`
	Hosts    map[string]Host   `toml:"hosts"`
	Peers    map[string]string `toml:"peers"`
}

type HostDefaults struct {
	Privilege     string `toml:"privilege,omitempty"`
	Scheduler     string `toml:"scheduler,omitempty"`
	Kpclientd     *bool  `toml:"kpclientd,omitempty"`
	KpclientdUser string `toml:"kpclientd_user,omitempty"`
	Install       string `toml:"install,omitempty"`
}

const (
	InstallDpkg = "dpkg"

	InstallPkg = "pkg"

	InstallDocker = "docker"

	InstallCompose = "compose"
)

var Installs = []string{InstallDpkg, InstallPkg, InstallDocker, InstallCompose}

func NormalizeInstall(v string) (string, bool) {
	if v == "" {
		return InstallDpkg, true
	}
	for _, m := range Installs {
		if v == m {
			return m, true
		}
	}
	return "", false
}

func (c Config) InstallMethod(label string) string {
	if h, ok := c.Hosts[label]; ok && h.Install != "" {
		return h.Install
	}
	if c.Defaults.Install != "" {
		return c.Defaults.Install
	}
	return InstallDpkg
}

func quoteList(xs []string) string {
	q := make([]string, len(xs))
	for i, x := range xs {
		q[i] = fmt.Sprintf("%q", x)
	}
	return strings.Join(q, ", ")
}

func Write(c Config, path string) error {
	var b strings.Builder
	b.WriteString("# written by kno setup (edit freely)\n")
	fmt.Fprintf(&b, "network = %q\n", c.Network)
	fmt.Fprintf(&b, "nodes = [%s]\n\n", quoteList(c.Nodes))
	fmt.Fprintf(&b, "[update]\npolicy = %q\nrequire_signed_tag = %v\n",
		c.Update.Policy, c.Update.RequireSignedTag)
	if c.Update.Threshold > 0 {
		fmt.Fprintf(&b, "threshold = %d\n", c.Update.Threshold)
	}
	if c.Update.RequireSignatures {
		b.WriteString("require_signatures = true\n")
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "[push]\nmode = %q\n", c.Push.Mode)
	if d := c.Defaults; d != (HostDefaults{}) {
		b.WriteString("\n# applies to every host below that does not say otherwise\n[defaults]\n")
		if d.Privilege != "" {
			fmt.Fprintf(&b, "privilege = %q\n", d.Privilege)
		}
		if d.Scheduler != "" {
			fmt.Fprintf(&b, "scheduler = %q\n", d.Scheduler)
		}
		if d.Kpclientd != nil {
			fmt.Fprintf(&b, "kpclientd = %v\n", *d.Kpclientd)
		}
		if d.KpclientdUser != "" {
			fmt.Fprintf(&b, "kpclientd_user = %q\n", d.KpclientdUser)
		}
		if d.Install != "" {
			fmt.Fprintf(&b, "install = %q\n", d.Install)
		}
	}
	names := make([]string, 0, len(c.Hosts))
	for n := range c.Hosts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		h := c.Hosts[n]
		fmt.Fprintf(&b, "\n[hosts.%q]\n", n)
		if h.SSH != "" {
			fmt.Fprintf(&b, "ssh = %q\n", h.SSH)
		}
		if h.Hostname != "" {
			fmt.Fprintf(&b, "hostname = %q\n", h.Hostname)
		}
		fmt.Fprintf(&b, "scheduler = %q\n", h.Scheduler)
		if h.Kpclientd != nil {
			fmt.Fprintf(&b, "kpclientd = %v\n", *h.Kpclientd)
		}
		if h.KpclientdUser != "" {
			fmt.Fprintf(&b, "kpclientd_user = %q\n", h.KpclientdUser)
		}
		if h.Install != "" {
			fmt.Fprintf(&b, "install = %q\n", h.Install)
		}
		if h.Privilege != "" {
			fmt.Fprintf(&b, "privilege = %q\n", h.Privilege)
		}
		if h.ClientJail != "" {
			fmt.Fprintf(&b, "client_jail = %q\n", h.ClientJail)
		}
		if len(h.Services) > 0 {
			fmt.Fprintf(&b, "services = [%s]\n", quoteList(h.Services))
		}
	}
	if len(c.Peers) > 0 {
		peers := make([]string, 0, len(c.Peers))
		for n := range c.Peers {
			peers = append(peers, n)
		}
		sort.Strings(peers)
		b.WriteString("\n[peers]\n")
		for _, n := range peers {
			fmt.Fprintf(&b, "%q = %q\n", n, c.Peers[n])
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func exists(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

var rootTrusts = fsutil.Trusted

func ResolvePath(flagPath string) string {
	if flagPath != "" && rootTrusts(flagPath) {
		return flagPath
	}
	if e := os.Getenv("KNO_CONFIG"); e != "" && rootTrusts(e) {
		return e
	}

	if h, _ := os.UserHomeDir(); h != "" && !runningAsUpdater() {
		p := filepath.Join(h, ".local", "katzenpost", "kno.toml")
		if exists(p) && rootTrusts(p) {
			return p
		}
	}

	return SystemPath()
}

func SystemConfigPath() string { return SystemConfigPathsFor(runtime.GOOS)[0] }

func SystemConfigPathsFor(goos string) []string {
	if goos == "freebsd" {
		return []string{"/usr/local/etc/kno.toml", "/usr/local/etc/katzenpost/kno.toml", "/etc/katzenpost/kno.toml"}
	}
	return []string{"/etc/katzenpost/kno.toml"}
}

var (
	warnedMu sync.Mutex
	warned   = map[string]bool{}
)

func Load(path string) (Config, error) {
	c, warns, err := load(path)
	if err != nil || len(warns) == 0 {
		return c, err
	}
	warnedMu.Lock()
	defer warnedMu.Unlock()
	if !warned[path] {
		warned[path] = true
		for _, w := range warns {
			fmt.Fprintf(os.Stderr, "kno: %s: %s\n", path, w)
		}
	}
	return c, nil
}

func load(path string) (Config, []string, error) {
	var c Config
	md, err := toml.DecodeFile(path, &c)
	if err != nil {
		return c, nil, err
	}
	var warns []string
	for _, k := range md.Undecoded() {
		warns = append(warns, fmt.Sprintf("unknown key %s; kno ignores it", k))
	}
	return c, append(warns, c.Problems()...), nil
}

var Schedulers = []string{"none", "timer", "cron"}

func (c Config) Problems() []string {
	var out []string
	check := func(where string, v HostDefaults) {
		if !ValidPrivilege(v.Privilege) {
			out = append(out, fmt.Sprintf("%s privilege %q is not one of %s; kno uses %s",
				where, v.Privilege, strings.Join(Privileges, ", "), c.defaultPrivilege()))
		}
		if _, ok := NormalizeInstall(v.Install); !ok {
			out = append(out, fmt.Sprintf("%s install %q is not one of %s",
				where, v.Install, strings.Join(Installs, ", ")))
		}
		if v.Scheduler != "" && !slices.Contains(Schedulers, v.Scheduler) {
			out = append(out, fmt.Sprintf("%s scheduler %q is not one of %s",
				where, v.Scheduler, strings.Join(Schedulers, ", ")))
		}
	}
	check("[defaults]", c.Defaults)
	names := make([]string, 0, len(c.Hosts))
	for n := range c.Hosts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		h := c.Hosts[n]
		check(fmt.Sprintf("[hosts.%q]", n),
			HostDefaults{Privilege: h.Privilege, Install: h.Install, Scheduler: h.Scheduler})
	}
	return out
}

func SystemPath() string {
	for _, p := range SystemConfigPathsFor(runtime.GOOS) {
		if exists(p) {
			return p
		}
	}
	return ""
}

func Masked(userPath, systemPath string) []string {
	if userPath == "" || systemPath == "" || userPath == systemPath {
		return nil
	}
	var u, s Config
	um, err := toml.DecodeFile(userPath, &u)
	if err != nil {
		return nil
	}
	sm, err := toml.DecodeFile(systemPath, &s)
	if err != nil {
		return nil
	}
	have := map[string]bool{}
	for _, k := range um.Keys() {
		have[k.String()] = true
	}
	var out []string
	for _, k := range sm.Keys() {
		key := k.String()
		if have[key] || !comparableKey(key, u) {
			continue
		}
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func comparableKey(key string, user Config) bool {
	rest, ok := strings.CutPrefix(key, "hosts.")
	if !ok {
		return true
	}
	label, _, _ := strings.Cut(rest, ".")
	label = strings.Trim(label, `"`)
	if label == "" {
		return false
	}
	_, mine := user.Hosts[label]
	return mine
}

const DataDir = "data"

func RepoDir() string {
	if r := os.Getenv("KNO_REPO"); r != "" {
		return dataRoot(r)
	}
	if d, ok := CheckoutDir(); ok {
		return d
	}
	if d, ok := OwnClone(os.Getenv("KNO_HOME"), os.Getenv("HOME")); ok {
		return d
	}
	if dir, err := EmbeddedRepo(); err == nil {
		return dir
	}
	wd, _ := os.Getwd()
	return wd
}

func OwnClone(homes ...string) (string, bool) {
	for _, h := range homes {
		if h == "" {
			continue
		}
		for _, n := range []string{"network-organizer", "katzenpost-network-organizer"} {
			if d, ok := DataRootOf(filepath.Join(h, n)); ok {
				return d, true
			}
		}
	}
	return "", false
}

func CheckoutDir() (string, bool) {
	wd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	return DataRootOf(wd)
}

func DataRootOf(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	if isCheckout(dir) {
		return dir, true
	}
	if d := filepath.Join(dir, DataDir); isCheckout(d) {
		return d, true
	}
	return "", false
}

func dataRoot(dir string) string {
	if d, ok := DataRootOf(dir); ok {
		return d
	}
	return dir
}

func isCheckout(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "networks"))
	return err == nil
}

func GitRoot(dataDir string) (string, bool) {
	out, err := exec.Command("git", "-C", dataDir, "rev-parse",
		"--show-toplevel").Output()
	if err != nil {
		return "", false
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", false
	}
	return root, true
}

func ownedTree(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || !fi.IsDir() {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {

		return false
	}
	return int(st.Uid) == os.Getuid()
}

func EmbeddedRepo() (string, error) {
	sum, err := knorepo.Digest()
	if err != nil {
		return "", err
	}
	cache := knopath.Cache()
	dir := filepath.Join(cache, "repo-"+sum)

	if ownedTree(filepath.Join(dir, "networks")) {
		return dir, nil
	}

	if _, err := os.Stat(dir); err == nil && !ownedTree(filepath.Join(dir, "networks")) {
		if err := os.RemoveAll(dir); err != nil {
			return "", fmt.Errorf("a repo cache at %s is not ours or not whole,"+
				" and cannot be replaced: %w", dir, err)
		}
	}
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(cache, "repo-"+sum+".tmp-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if err := knorepo.Materialize(tmp); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return "", err
	}

	if err := os.Rename(tmp, dir); err != nil {
		if ownedTree(filepath.Join(dir, "networks")) {
			return dir, nil
		}
		return "", err
	}
	return dir, nil
}

func ListNodes(repo, network string) []string {
	dir := knopath.ConfigsDir(repo, network)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range ents {
		p, ok := nodefile.NodeOf(e.Name())
		if !ok {
			continue
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func ClaimedServices(repo, network string, nodes []string) []string {
	dir := knopath.ConfigsDir(repo, network)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	want := map[string]bool{}
	for _, n := range nodes {
		want[n] = true
	}
	var out []string
	for _, e := range ents {
		n := strings.TrimSuffix(e.Name(), ".toml")
		if !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		if node, ok := nodefile.NodeOf(n); ok && want[node] {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func ClaimedHosts(repo string, c Config) []string {
	var out []string
	claimed := map[string]bool{}
	for _, s := range ClaimedServices(repo, NetworkOf(c), c.Nodes) {
		claimed[s] = true
	}
	for label, h := range c.Hosts {
		svcs := HostServices(repo, c, h)
		if len(svcs) == 0 {
			continue
		}
		if len(claimed) == 0 {
			out = append(out, label)
			continue
		}
		for _, s := range svcs {
			if claimed[s] {
				out = append(out, label)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

func UnclaimedHosts(repo string, c Config) []string {
	in := map[string]bool{}
	for _, l := range ClaimedHosts(repo, c) {
		in[l] = true
	}
	var out []string
	for label, h := range c.Hosts {
		if in[label] || len(HostServices(repo, c, h)) == 0 {
			continue
		}
		out = append(out, label)
	}
	sort.Strings(out)
	return out
}

type networkFile struct {
	Default   string            `toml:"default"`
	DefaultOS string            `toml:"default_os"`
	Arch      map[string]string `toml:"arch"`
	OS        map[string]string `toml:"os"`
	Operators map[string]struct {
		Nodes []string `toml:"nodes"`
	} `toml:"operators"`
}

func readNetworkFile(repo, network string) (networkFile, error) {
	var n networkFile
	_, err := toml.DecodeFile(knopath.NetworkFile(repo, network), &n)
	return n, err
}

func LoadOperators(repo, network string) map[string][]string {
	n, err := readNetworkFile(repo, network)
	if err != nil {
		return nil
	}
	out := map[string][]string{}
	for name, v := range n.Operators {
		out[name] = v.Nodes
	}
	return out
}

func NetworkArches(repo, network string) []string {
	a, _ := readNetworkFile(repo, network)
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		out = append(out, v)
	}
	add(a.Default)
	for _, v := range a.Arch {
		add(v)
	}
	sort.Strings(out)
	return out
}

func NodeArch(repo, network, node string) string {
	a, _ := readNetworkFile(repo, network)
	if v, ok := a.Arch[node]; ok && v != "" {
		return v
	}
	if a.Default != "" {
		return a.Default
	}
	return "amd64"
}

func NodeOS(repo, network, node string) string {
	a, _ := readNetworkFile(repo, network)
	if v, ok := a.OS[node]; ok && v != "" {
		return v
	}
	if a.DefaultOS != "" {
		return a.DefaultOS
	}
	return "debian-13"
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func NetworkOf(c Config) string {
	if c.Network == "" {
		return "namenlos"
	}
	return c.Network
}

func LocalHost(c Config) (string, Host, bool) {
	if e := os.Getenv("KNO_HOST"); e != "" {
		if h, ok := c.Hosts[e]; ok {
			return e, h, true
		}
		return "", Host{}, false
	}
	if hn, err := os.Hostname(); err == nil && hn != "" {
		if label, many := hostNamed(c, hn); label != "" {
			return label, c.Hosts[label], true
		} else if len(many) > 0 {
			return "", Host{}, false
		}
	}
	if len(c.Hosts) == 1 {
		for label, h := range c.Hosts {
			if h.SSH == "" {
				return label, h, true
			}
		}
	}
	return "", Host{}, false
}

func hostNamed(c Config, hn string) (string, []string) {
	short := hn
	if i := strings.Index(hn, "."); i > 0 {
		short = hn[:i]
	}
	if _, ok := c.Hosts[hn]; ok {
		return hn, nil
	}
	if _, ok := c.Hosts[short]; ok {
		return short, nil
	}
	for _, name := range []string{hn, short} {
		var hits []string
		for label, h := range c.Hosts {
			if h.Hostname == name {
				hits = append(hits, label)
			}
		}
		sort.Strings(hits)
		switch len(hits) {
		case 0:
			continue
		case 1:
			return hits[0], nil
		default:
			return "", hits
		}
	}
	return "", nil
}

func HostServices(repo string, c Config, h Host) []string {
	if len(h.Services) > 0 {
		return h.Services
	}
	if len(c.Hosts) == 1 {
		return ClaimedServices(repo, NetworkOf(c), c.Nodes)
	}
	return nil
}

func PlacementIssues(repo string, c Config) []string {
	out := unknownServices(repo, c)
	if len(c.Hosts) <= 1 {
		return out
	}
	count := map[string]int{}
	for _, h := range c.Hosts {
		for _, s := range h.Services {
			count[s]++
		}
	}
	for _, s := range ClaimedServices(repo, NetworkOf(c), c.Nodes) {
		switch count[s] {
		case 0:
			out = append(out, "claimed but not placed on any host: "+s)
		case 1:
		default:
			out = append(out, fmt.Sprintf("placed on %d hosts: %s", count[s], s))
		}
	}
	return out
}

func unknownServices(repo string, c Config) []string {
	dir := knopath.ConfigsDir(repo, NetworkOf(c))
	if _, err := os.Stat(dir); err != nil {
		return nil
	}
	names := make([]string, 0, len(c.Hosts))
	for n := range c.Hosts {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []string
	for _, n := range names {
		for _, s := range c.Hosts[n].Services {
			if _, err := os.Stat(filepath.Join(dir, s+".toml")); err != nil {
				out = append(out, fmt.Sprintf("host %s names a service with no generated config: %s", n, s))
			}
		}
	}
	return out
}

func emitSh(c Config, repo, hostLabel string) {
	label := hostLabel
	var h Host
	haveHost := false
	if label != "" {
		h, haveHost = c.Hosts[label], true
	} else if l, hh, ok := LocalHost(c); ok {
		label, h, haveHost = l, hh, true
	}
	svcs := ""
	if haveHost {
		svcs = strings.Join(HostServices(repo, c, h), " ")
	}
	policy := c.Update.Policy
	if policy == "" {
		policy = "apply"
	}
	mode := c.Push.Mode
	if mode == "" {
		mode = "full"
	}
	verifyTag := "0"
	if c.Update.RequireSignedTag {
		verifyTag = "1"
	}
	arch, osname := "", ""
	if svcs != "" {
		n, _ := nodefile.NodeOf(strings.Fields(svcs)[0])
		arch = NodeArch(repo, NetworkOf(c), n)
		osname = NodeOS(repo, NetworkOf(c), n)
	}
	fmt.Printf("KNO_NETWORK=%s\n", shQuote(NetworkOf(c)))
	fmt.Printf("SERVICES=%s\n", shQuote(svcs))
	fmt.Printf("KNO_HOST=%s\n", shQuote(label))
	fmt.Printf("KNO_HOST_SSH=%s\n", shQuote(h.SSH))
	fmt.Printf("KNO_HOST_ARCH=%s\n", shQuote(arch))
	fmt.Printf("KNO_HOST_OS=%s\n", shQuote(osname))
	fmt.Printf("KNO_UPDATE_POLICY=%s\n", shQuote(policy))
	fmt.Printf("KNO_PUSH_MODE=%s\n", shQuote(mode))
	fmt.Printf("VERIFY_TAG=%s\n", shQuote(verifyTag))
}

func Summary(w io.Writer, path string, c Config, repo string) {
	fmt.Fprintf(w, "config:  %s\n", path)
	fmt.Fprintf(w, "network: %s\n", NetworkOf(c))
	fmt.Fprintf(w, "nodes:   %s\n", strings.Join(c.Nodes, " "))
	fmt.Fprintf(w, "update:  policy=%s require_signed_tag=%v require_signatures=%v\n",
		c.Update.Policy, c.Update.RequireSignedTag, c.Update.RequireSignatures)
	fmt.Fprintf(w, "push:    mode=%s\n", c.Push.Mode)
	locLabel, _, haveLocal := LocalHost(c)
	names := make([]string, 0, len(c.Hosts))
	for n := range c.Hosts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		h := c.Hosts[n]
		where := h.SSH
		if where == "" {
			where = "local"
		}
		mark := " "
		if n == locLabel {
			mark = "*"
		}

		client := ""
		if c.RunsKpclientd(n) {
			client = " kpclientd=yes"
			if u := c.KpclientdUser(n); u != "" {
				client = " kpclientd=" + u
			}
		}
		priv := ""
		if p := c.Privilege(n); p != PrivNone {
			priv = " privilege=" + p
		}
		fmt.Fprintf(w, "host %s %s: %s scheduler=%s%s%s\n",
			mark, n, where, c.Scheduler(n), client, priv)
		const lead = "         services: "

		body := cli.Wrap(strings.Join(HostServices(repo, c, h), " "),
			cli.Width-2-len(lead), "")
		for i, line := range strings.Split(body, "\n") {
			prefix := lead
			if i > 0 {
				prefix = strings.Repeat(" ", len(lead))
			}
			fmt.Fprintf(w, "%s%s\n", prefix, line)
		}
	}
	if !haveLocal {
		fmt.Fprintf(w, "host:    none: this host runs no node, and it organizes the hosts above\n")
		if hn, err := os.Hostname(); err == nil && os.Getenv("KNO_HOST") == "" {
			if _, many := hostNamed(c, hn); len(many) > 0 {
				fmt.Fprintf(w, "WARN:    the hosts %s all have the hostname of this host (%s)."+
					" Set KNO_HOST to the one this host is\n", strings.Join(many, " "), hn)
			}
		}
	}
	pnames := make([]string, 0, len(c.Peers))
	for n := range c.Peers {
		pnames = append(pnames, n)
	}
	sort.Strings(pnames)
	for _, n := range pnames {
		fmt.Fprintf(w, "peer     %s -> %s\n", n, c.Peers[n])
	}
	for _, warn := range PlacementIssues(repo, c) {
		fmt.Fprintf(w, "WARN:    %s\n", warn)
	}
}

const UpdaterAccount = "kno"

func runningAsUpdater() bool {
	u, err := user.Current()
	if err != nil {
		return false
	}
	return u.Username == UpdaterAccount
}

const DefaultThreshold = 1

func (c Config) SignatureThreshold() int {
	if c.Update.Threshold > 0 {
		return c.Update.Threshold
	}
	return DefaultThreshold
}
