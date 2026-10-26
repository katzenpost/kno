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

const (
	NativeBuild = "native"
	CrossBuild  = "cross"
)

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

func (s PackageSums) kind() string {
	if s.Build == "" {
		return NativeBuild
	}
	return s.Build
}

func buildKind(dir string) string {
	m, _ := LoadManifest(filepath.Join(dir, ManifestName))
	return m.kind()
}

func kindDir(repo, network, tag, kind string) string {
	dir := CommittedDir(repo, network, tag)
	_, err := os.Stat(filepath.Join(dir, SumsName))
	if err != nil || buildKind(dir) == kind {
		return dir
	}
	return filepath.Join(dir, kind)
}

func committedEntries(repo, network, tag, kind string) ([]PackageSum, error) {
	top := filepath.Join(kindDir(repo, network, tag, kind), SumsName)
	entries, err := SumsEntries(top)
	if err != nil {
		return nil, err
	}
	named := make(map[string]bool, len(entries))
	for _, e := range entries {
		named[e.Name] = true
	}
	for _, d := range nodeRecordDirs(CommittedDir(repo, network, tag)) {
		more, err := SumsEntries(filepath.Join(d, SumsName))
		if err != nil {
			continue
		}
		for _, e := range more {
			if !named[e.Name] {
				entries = append(entries, e)
			}
		}
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
