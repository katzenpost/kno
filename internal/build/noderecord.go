package build

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/pkg/freebsd"
)

const NodesDir = "nodes"

func NodeOfPackage(name string) string {
	svc, ok := configPkgService(name)
	if !ok {
		stem, isPkg := freebsd.SplitPkgName(name)
		svc, ok = strings.CutPrefix(stem, knopath.ConfigPkgPrefix)
		if !isPkg || !ok {
			return ""
		}
	}
	node, _, ok := strings.Cut(svc, "-pq-")
	if !ok {
		return ""
	}
	return node
}

func NodeRecordDir(dir, node string) string { return filepath.Join(dir, NodesDir, node) }

func nodeRecordDirs(dir string) []string {
	ents, err := os.ReadDir(filepath.Join(dir, NodesDir))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, filepath.Join(dir, NodesDir, e.Name()))
		}
	}
	sort.Strings(out)
	return out
}

func committedEntries(repo, network, tag string) ([]PackageSum, error) {
	sums, _ := CommittedSums(repo, network, tag)
	entries, err := SumsEntries(sums)
	if err != nil {
		return nil, err
	}
	for _, d := range nodeRecordDirs(CommittedDir(repo, network, tag)) {
		more, err := SumsEntries(filepath.Join(d, SumsName))
		if err != nil {
			continue
		}
		entries = append(entries, more...)
	}
	return entries, nil
}

func splitByNode(pkgs []PackageSum) (top []PackageSum, nodes map[string][]PackageSum) {
	nodes = map[string][]PackageSum{}
	for _, p := range pkgs {
		if n := NodeOfPackage(p.Name); n != "" {
			nodes[n] = append(nodes[n], p)
			continue
		}
		top = append(top, p)
	}
	return top, nodes
}
