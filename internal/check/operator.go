package check

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/service"
)

type OperatorOptions struct {
	Repo, Network string

	Operators []string

	SpotChecks []SpotCheck
}

type SpotCheck struct {
	Node string
	Arch string
	OS   string

	Paths map[string]string
}

func NamenlosSpotChecks() []SpotCheck {
	return []SpotCheck{
		{
			Node: "oneeyedman", Arch: "arm64", OS: "debian-13",
			Paths: map[string]string{
				"oneeyedman-pq-mixserver": "/etc/katzenpost/server/oneeyedman-pq-mixserver.toml:/var/lib/katzenpost/server/oneeyedman-pq-mixserver",
			},
		},
		{
			Node: "luscus", Arch: "amd64", OS: "debian-13",
			Paths: map[string]string{
				"luscus-pq-authority": "/etc/katzenpost/authority/luscus-pq-authority.toml:/var/lib/katzenpost/authority/luscus-pq-authority",
			},
		},
		{
			Node: "annares", Arch: "ppc64el", OS: "ubuntu-26.04",
			Paths: map[string]string{
				"annares-pq-storagenode-0": "/etc/katzenpost/replica/annares-pq-storagenode-0.toml:/var/lib/katzenpost/replica/annares-pq-storagenode-0",
				"annares-pq-storagenode-1": "/etc/katzenpost/replica/annares-pq-storagenode-1.toml:/var/lib/katzenpost/replica/annares-pq-storagenode-1",
			},
		},
	}
}

func Operator(o OperatorOptions) (Result, error) {
	var r Result
	if o.Repo == "" || o.Network == "" {
		return r, fmt.Errorf("operator check: repo and network are required")
	}
	ops := config.LoadOperators(o.Repo, o.Network)
	if len(ops) == 0 {
		return r, fmt.Errorf("operator check: no operator in networks/%s/editable-configs/machines/", o.Network)
	}
	names := make([]string, 0, len(ops))
	for n := range ops {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(o.Operators) > 0 {
		for _, want := range o.Operators {
			if _, ok := ops[want]; !ok {
				return r, fmt.Errorf("operator check: no operator %q in networks/%s/editable-configs/machines/", want, o.Network)
			}
		}
		names = append([]string{}, o.Operators...)
		sort.Strings(names)
	}

	topo := map[string]bool{}
	for _, s := range config.ClaimedServices(o.Repo, o.Network, config.ListNodes(o.Repo, o.Network)) {
		topo[s] = true
	}
	if len(topo) == 0 {
		return r, fmt.Errorf("operator check: no services in networks/%s/.generated/configs", o.Network)
	}

	for _, op := range names {
		nodes := ops[op]
		lr, err := checkLaptop(o.Repo, o.Network, op, nodes)
		if err != nil {
			return r, err
		}
		r.merge(lr)
		for _, n := range nodes {
			r.merge(checkNode(o.Repo, o.Network, n, topo))
		}
	}

	if len(o.Operators) == 0 {
		r.merge(checkPlacement(o.Repo, o.Network, names, ops, topo))
	}

	spots := o.SpotChecks
	if spots == nil && o.Network == "namenlos" {
		spots = NamenlosSpotChecks()
	}
	if len(spots) == 0 {
		r.add("operator", "pinned spot checks", Skip, "no exact values pinned for network %s", o.Network)
		return r, nil
	}
	for _, s := range spots {
		r.eq("operator", "pinned "+s.Node+" arch", config.NodeArch(o.Repo, o.Network, s.Node), s.Arch)
		r.eq("operator", "pinned "+s.Node+" os", config.NodeOS(o.Repo, o.Network, s.Node), s.OS)
		svcs := make([]string, 0, len(s.Paths))
		for svc := range s.Paths {
			svcs = append(svcs, svc)
		}
		sort.Strings(svcs)
		for _, svc := range svcs {
			got := "unresolved"
			if sv, err := service.ResolveService(o.Repo, o.Network, svc); err == nil {
				got = sv.ConfigDst + ":" + sv.StateDir
			}
			r.eq("operator", "pinned "+svc+" paths", got, s.Paths[svc])
		}
	}
	return r, nil
}

func checkLaptop(repo, network, op string, nodes []string) (Result, error) {
	var r Result
	c := config.Config{Network: network, Nodes: nodes, Hosts: map[string]config.Host{}}
	for _, n := range nodes {
		c.Hosts[n] = config.Host{SSH: "root@" + n, Services: nodeServices(repo, network, n)}
	}
	dir, err := os.MkdirTemp("", "kno-operator-check-")
	if err != nil {
		return r, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	p := filepath.Join(dir, op+".laptop.toml")
	if err := config.Write(c, p); err != nil {
		r.add("operator", op+" laptop config", Fail, "cannot be written: %v", err)
		return r, nil
	}
	got, err := config.Load(p)
	if err != nil {
		r.add("operator", op+" laptop config", Fail, "does not resolve: %v", err)
		return r, nil
	}
	if len(got.Hosts) != len(nodes) {
		r.add("operator", op+" laptop config", Fail, "resolves to %d hosts, not %d", len(got.Hosts), len(nodes))
		return r, nil
	}

	for _, issue := range config.PlacementIssues(repo, got) {
		r.add("operator", op+" placement: "+issue, Fail, "the group's config does not place its claimed services once each")
	}
	r.add("operator", op+" laptop config", Pass, "resolves (%s)", strings.Join(nodes, " "))
	return r, nil
}

func checkPlacement(repo, network string, names []string, ops map[string][]string, topo map[string]bool) Result {
	var r Result
	owners := map[string][]string{}
	for _, op := range names {
		for _, n := range ops[op] {
			for _, s := range nodeServices(repo, network, n) {
				owners[s] = append(owners[s], op)
			}
		}
	}
	svcs := make([]string, 0, len(topo))
	for s := range topo {
		svcs = append(svcs, s)
	}
	sort.Strings(svcs)
	bad := 0
	for _, s := range svcs {
		who := owners[s]
		switch len(who) {
		case 1:
		case 0:
			r.add("operator", "placement "+s, Fail, "in the network topology but no operator group claims it")
			bad++
		default:
			r.add("operator", "placement "+s, Fail, "claimed by %d groups: %s", len(who), strings.Join(who, " "))
			bad++
		}
	}
	if bad == 0 {
		r.add("operator", "placement", Pass, "all %d service(s) claimed by exactly one group", len(svcs))
	}
	return r
}

func checkNode(repo, network, node string, topo map[string]bool) Result {
	var r Result
	svcs := nodeServices(repo, network, node)
	if len(svcs) == 0 {
		r.add("operator", node, Fail, "no services resolve")
		return r
	}
	r.add("operator", node+" services", Pass, "%s", strings.Join(svcs, " "))
	for _, s := range svcs {
		if !topo[s] {
			r.add("operator", node+" "+s, Fail, "not in the network topology (no .generated/configs/%s.toml)", s)
			continue
		}
		sv, err := service.ResolveService(repo, network, s)
		if err != nil {
			r.add("operator", node+" "+s, Fail, "does not resolve: %v", err)
			continue
		}
		if sv.ConfigDst == "" || sv.StateDir == "" {
			r.add("operator", node+" "+s, Fail, "resolves to no config or state path")
			continue
		}
		if err := dataDirMatches(repo, network, sv); err != nil {
			r.add("operator", node+" "+s, Fail, "%v", err)
			continue
		}
		r.add("operator", node+" "+s, Pass, "%s %s", sv.ConfigDst, sv.StateDir)
	}
	arch := config.NodeArch(repo, network, node)
	if arch == "" {
		r.add("operator", node+" arch", Fail, "empty")
	} else {
		r.add("operator", node+" arch", Pass, "%s", arch)
	}
	osName := config.NodeOS(repo, network, node)
	if osName == "" {
		r.add("operator", node+" os", Fail, "empty")
	} else {
		r.add("operator", node+" os", Pass, "%s (metadata; one bookworm image builds all)", osName)
	}

	goarch, cc, err := compile.ArchToGo(arch)
	if err != nil {
		r.add("operator", node+" build", Fail, "%v", err)
		return r
	}
	r.add("operator", node+" build", Pass, "%s -> GOARCH=%s CC=%s", arch, goarch, cc)
	return r
}

func nodeServices(repo, network, node string) []string {
	c := config.Config{
		Network: network,
		Nodes:   []string{node},
		Hosts:   map[string]config.Host{node: {Hostname: node, Scheduler: "none"}},
	}
	return config.HostServices(repo, c, c.Hosts[node])
}

var dataDirLine = regexp.MustCompile(`(?m)^\s*DataDir\s*=\s*"([^"]+)"`)

func dataDirMatches(repo, network string, sv service.Service) error {
	if sv.Backend != "systemd" || sv.StateDir == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(knopath.ConfigsDir(repo, network), sv.Name+".toml"))
	if err != nil {
		return err
	}
	m := dataDirLine.FindSubmatch(b)
	if m == nil {
		return fmt.Errorf("the rendered config names no DataDir")
	}
	if got := strings.TrimSuffix(string(m[1]), "/"); got != strings.TrimSuffix(sv.StateDir, "/") {
		return fmt.Errorf("DataDir is %s, but its unit keeps state in %s", got, sv.StateDir)
	}
	return nil
}
