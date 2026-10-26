package nodeedit

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/contribute"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/nodefile"
)

type Node struct {
	Node string
	File string
	Path string
}

func Mine(repo, network string, claim []string) []Node {
	index := contribute.Owners(repo, network)
	dir := filepath.Join(knopath.EditableDir(repo, network), "nodes")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Node
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		rel := filepath.Join("networks", network, knopath.EditableName, "nodes", e.Name())
		n, ok := contribute.OwnerOf(rel, claim, index)
		if !ok {
			continue
		}
		out = append(out, Node{Node: n, File: e.Name(),
			Path: filepath.Join(dir, e.Name())})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out
}

func Report(w io.Writer, mine []Node) {
	if len(mine) == 0 {
		fmt.Fprintln(w, "kno edit: this config claims no node with a file here")
		return
	}
	for i, n := range mine {
		addrs, err := nodefile.Addresses(n.Path)
		if err != nil {
			fmt.Fprintf(w, "%2d. %-34s %v\n", i+1, n.File, err)
			continue
		}
		fmt.Fprintf(w, "%2d. %-34s %s\n", i+1, n.File, strings.Join(addrs, " "))
	}
}

func Pick(mine []Node, want string) (Node, error) {
	if len(mine) == 0 {
		return Node{}, fmt.Errorf("this config claims no node with a file" +
			" here.\n  'kno setup' is where a node is claimed.")
	}
	if want == "" {
		if len(mine) == 1 {
			return mine[0], nil
		}
		return Node{}, fmt.Errorf("this config claims %d nodes. Name one"+
			" with --node", len(mine))
	}
	var names, hits []string
	var hit Node
	for _, n := range mine {
		file := strings.TrimSuffix(n.File, ".toml")
		if n.File == want || file == want {
			return n, nil
		}
		if n.Node == want {
			hit = n
			hits = append(hits, file)
		}
		names = append(names, file)
	}
	switch len(hits) {
	case 0:
		return Node{}, fmt.Errorf("%q is not a node this config claims. Yours:"+
			" %s", want, strings.Join(names, " "))
	case 1:
		return hit, nil
	}
	return Node{}, fmt.Errorf("%q has %d node files: %s. Name one of them with --node",
		want, len(hits), strings.Join(hits, " "))
}

func ParseAddresses(in string) ([]string, error) {
	in = strings.ReplaceAll(in, ",", " ")
	fields := strings.Fields(in)
	if len(fields) == 0 {
		return nil, fmt.Errorf("a node needs at least one address")
	}
	for _, f := range fields {
		if err := nodefile.ValidAddress(f); err != nil {
			return nil, err
		}
	}
	return fields, nil
}
