package topology

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/nodefile"

	"github.com/BurntSushi/toml"

	"github.com/katzenpost/kno/internal/peers"
)

type Finding struct {
	Subject string
	Detail  string
}

func (f Finding) String() string { return f.Subject + ": " + f.Detail }

var idKeyDir = map[string]string{
	"mix":         "mix-id-keys",
	"gateway":     "gateway-id-keys",
	"servicenode": "servicenode-id-keys",
	"storagenode": "replica-id-keys",
}

const identityHeader = "-----BEGIN ED25519 SPHINCS+ PUBLIC KEY-----"

type lintNode struct {
	Name            string
	Role            string
	ConfigName      string
	IdentityKeyFile string
	MetricsAddress  string
	Addresses       []string
	ReplicaID       *int
	file            string
}

func wildcard(addr string) bool {
	a := addr
	if _, rest, ok := strings.Cut(a, "://"); ok {
		a = rest
	}
	host := a
	if i := strings.LastIndex(a, ":"); i > 0 {
		host = a[:i]
	}
	host = strings.Trim(host, "[]")
	switch host {
	case "0.0.0.0", "::", "":
		return true
	}
	return false
}

func lanBindFindings(repo, network string, add func(string, string, ...any)) {
	ms, err := nodefile.Machines(knopath.EditableDir(repo, network))
	if err != nil {
		return
	}
	gc := knopath.ConfigsDir(repo, network)
	seen := map[string]bool{}
	for _, g := range nodefile.LANGroups(ms) {
		for _, n := range g {
			if seen[n] {
				continue
			}
			seen[n] = true
			m, _ := filepath.Glob(filepath.Join(gc, n+"-pq-*.toml"))
			if len(m) == 0 {
				continue
			}
			bind := peers.OwnBind(m[0])
			switch {
			case bind == "":
				add(n, "shares a private network (its lan in machines/) but its config binds nothing,"+
					" so its neighbours are given no address to dial")
			case wildcard(bind):
				add(n, "shares a private network (its lan in machines/) and binds %s, which is what"+
					" its neighbours are told to dial. Give it the node's own private"+
					" address in nodes/", bind)
			}
		}
	}
}

func loadNodes(repo, network string) ([]lintNode, error) {
	dir := filepath.Join(knopath.EditableDir(repo, network), "nodes")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []lintNode
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		var n lintNode
		if _, err := toml.DecodeFile(p, &n); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		n.file = filepath.Join("nodes", e.Name())
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].file < out[j].file })
	return out, nil
}

func firstLine(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	if s.Scan() {
		return strings.TrimRight(s.Text(), "\r"), nil
	}
	return "", s.Err()
}

func Lint(repo, network string) ([]Finding, error) {
	nodes, err := loadNodes(repo, network)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no node fragments under networks/%s/nodes", network)
	}
	var out []Finding
	add := func(subject, format string, a ...any) {
		out = append(out, Finding{Subject: subject, Detail: fmt.Sprintf(format, a...)})
	}
	keyRoot := filepath.Join(knopath.EditableDir(repo, network), "keys")
	lanBindFindings(repo, network, add)
	out = append(out, lintMetricsCollisions(nodes)...)
	out = append(out, lintAddresses(nodes)...)

	referenced := map[string]bool{}
	for _, n := range nodes {
		switch {
		case n.Name == "":
			add(n.file, "no name")
			continue
		case n.Role == "":
			add(n.Name, "no role (%s)", n.file)
			continue
		case !nodefile.KnownRole(n.Role):
			add(n.Name, "role %q is not one of %s (%s)", n.Role,
				strings.Join(nodefile.Roles, ", "), n.file)
			continue
		}
		sub, ok := idKeyDir[n.Role]
		if !ok {
			continue
		}
		if n.IdentityKeyFile == "" {
			add(n.Name, "role %s but no identityKeyFile (%s)", n.Role, n.file)
			continue
		}
		p := filepath.Join(keyRoot, sub, n.IdentityKeyFile)
		referenced[filepath.Join(sub, n.IdentityKeyFile)] = true
		head, err := firstLine(p)
		if err != nil {
			add(n.Name, "identity key %s not found in keys/%s", n.IdentityKeyFile, sub)
			continue
		}
		if head != identityHeader {

			add(n.Name, "identity key %s is not an Ed25519 Sphincs+ identity key (starts %q)",
				n.IdentityKeyFile, head)
		}
	}

	seenRoleName, seenConfig := map[string]string{}, map[string]string{}
	for _, n := range nodes {
		if n.Name != "" && n.Role != "" {
			k := n.Role + "/" + n.Name
			if prev, dup := seenRoleName[k]; dup {
				add(n.Name, "a second %s node has this name (%s and %s)", n.Role, prev, n.file)
			}
			seenRoleName[k] = n.file
		}
		if n.ConfigName == "" {
			add(n.Name, "no configName (%s). Nothing can refer to this node", n.file)
			continue
		}
		if prev, dup := seenConfig[n.ConfigName]; dup {
			add(n.Name, "configName %s also used by %s", n.ConfigName, prev)
		}
		seenConfig[n.ConfigName] = n.file
	}

	out = append(out, lintManifestNodes(repo, network, nodes)...)
	out = append(out, lintReplicaIDs(nodes)...)
	out = append(out, lintLayers(repo, network, nodes)...)
	out = append(out, lintOrphanKeys(keyRoot, referenced)...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Subject != out[j].Subject {
			return out[i].Subject < out[j].Subject
		}
		return out[i].Detail < out[j].Detail
	})
	return out, nil
}

func lintManifestNodes(repo, network string, nodes []lintNode) []Finding {
	var m manifestT
	if _, err := toml.DecodeFile(ManifestPath(repo, network), &m); err != nil {
		return []Finding{{Subject: network, Detail: fmt.Sprintf("cannot read the manifest: %v", err)}}
	}
	listed := map[string]manifestNode{}
	for _, ref := range m.Node {
		listed[filepath.ToSlash(filepath.Clean(ref.Config))] = ref
	}
	var out []Finding
	byFile := map[string]bool{}
	for _, n := range nodes {
		file := filepath.ToSlash(n.file)
		byFile[file] = true
		ref, ok := listed[file]
		switch {
		case !ok:
			out = append(out, Finding{Subject: file,
				Detail: "no [[node]] in manifest.toml names this file, so kno render ignores it"})
		case ref.Name != n.Name || ref.Role != n.Role:
			out = append(out, Finding{Subject: file,
				Detail: fmt.Sprintf("manifest.toml names %s (%s), but the file says %s (%s)",
					ref.Name, ref.Role, n.Name, n.Role)})
		}
	}
	files := make([]string, 0, len(listed))
	for f := range listed {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		if !byFile[f] {
			out = append(out, Finding{Subject: f,
				Detail: fmt.Sprintf("manifest.toml names %s for %s, and there is no such node file",
					f, listed[f].Name)})
		}
	}
	return out
}

func lintReplicaIDs(nodes []lintNode) []Finding {
	var out []Finding
	seen := map[int]string{}
	for _, n := range nodes {
		if n.Role != "storagenode" {
			continue
		}
		if n.ReplicaID == nil {
			out = append(out, Finding{Subject: n.Name,
				Detail: fmt.Sprintf("storage node without replicaID (%s); it renders as replica 0", n.file)})
			continue
		}
		if prev, dup := seen[*n.ReplicaID]; dup {
			out = append(out, Finding{Subject: n.Name,
				Detail: fmt.Sprintf("replicaID %d also used by %s", *n.ReplicaID, prev)})
			continue
		}
		seen[*n.ReplicaID] = n.file
	}
	return out
}

func lintLayers(repo, network string, nodes []lintNode) []Finding {
	var out []Finding
	add := func(subject, format string, a ...any) {
		out = append(out, Finding{Subject: subject, Detail: fmt.Sprintf(format, a...)})
	}
	layers, err := Layers(ManifestPath(repo, network))
	if err != nil {
		add(network, "cannot read the topology layers: %v", err)
		return out
	}
	mixes := map[string]bool{}
	for _, n := range nodes {
		if n.Role == "mix" {
			mixes[n.Name] = true
		}
	}
	count := map[string]int{}
	for i, l := range layers {
		if len(l) == 0 {
			add(network, "topology layer %d is empty", i)
		}
		for _, m := range l {
			count[m]++
			if !mixes[m] {
				add(m, "topology layer %d names a node that is not a mix in this network", i)
			}
		}
	}
	names := make([]string, 0, len(mixes))
	for m := range mixes {
		names = append(names, m)
	}
	sort.Strings(names)
	for _, m := range names {
		switch c := count[m]; {
		case c == 0:
			add(m, "mix is in no topology layer, so it carries no traffic"+
				" (run 'kno topology-propose')")
		case c > 1:
			add(m, "mix appears in %d topology layers, expected 1", c)
		}
	}
	return out
}

func lintOrphanKeys(keyRoot string, referenced map[string]bool) []Finding {
	var out []Finding
	subs := make([]string, 0, len(idKeyDir))
	for _, sub := range idKeyDir {
		subs = append(subs, sub)
	}
	sort.Strings(subs)
	for _, sub := range subs {
		ents, err := os.ReadDir(filepath.Join(keyRoot, sub))
		if err != nil {
			continue
		}
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".pem") {
				continue
			}
			rel := filepath.Join(sub, e.Name())
			if !referenced[rel] {
				out = append(out, Finding{
					Subject: strings.TrimSuffix(e.Name(), ".pem"),
					Detail:  "identity key keys/" + rel + " is referenced by no node",
				})
			}
		}
	}
	return out
}

func KnownFindings(repo string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(repo, "topology-findings.txt"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out, nil
}

func Known(known []string, f Finding) bool {
	for _, k := range known {
		if f.Subject != "" && k == f.Subject {
			return true
		}
	}
	return false
}

func lintMetricsCollisions(nodes []lintNode) []Finding {
	seen := map[string][]string{}
	for _, n := range nodes {
		if n.MetricsAddress == "" {
			continue
		}
		who := n.ConfigName
		if who == "" {
			who = n.Name
		}
		seen[n.MetricsAddress] = append(seen[n.MetricsAddress], who)
	}
	var addrs []string
	for a, who := range seen {
		if len(who) > 1 {
			addrs = append(addrs, a)
		}
	}
	sort.Strings(addrs)
	var out []Finding
	for _, a := range addrs {
		who := seen[a]
		sort.Strings(who)
		out = append(out, Finding{
			Subject: "metrics address " + a,
			Detail: fmt.Sprintf("%s all bind it; two on one host means the second"+
				" panics with \"address already in use\" and restarts forever."+
				" Give each its own port, or remove metricsAddress from the ones"+
				" that do not need it", strings.Join(who, ", ")),
		})
	}
	return out
}

func lintAddresses(nodes []lintNode) []Finding {
	var out []Finding
	for _, n := range nodes {
		if len(n.Addresses) == 0 {
			out = append(out, Finding{Subject: n.file,
				Detail: "no addresses; the node cannot be reached"})
			continue
		}
		for _, a := range n.Addresses {
			if err := nodefile.ValidAddress(a); err != nil {
				out = append(out, Finding{Subject: n.file, Detail: err.Error()})
			}
		}
	}
	return out
}
