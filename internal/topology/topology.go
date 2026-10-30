package topology

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/katzenpost/kno/internal/knopath"
)

type Options struct {
	Repo, Network string

	Ask bool

	In io.Reader

	Log io.Writer
}

type Placement struct {
	Mix   string
	Layer int
}

type Proposal struct {
	Placements []Placement
	Manifest   string
	Diff       string
}

func (o Options) log() io.Writer {
	if o.Log != nil {
		return o.Log
	}
	return os.Stderr
}

func (o Options) in() io.Reader {
	if o.In != nil {
		return o.In
	}
	return os.Stdin
}

func ManifestPath(repo, network string) string {
	return filepath.Join(knopath.EditableDir(repo, network), "manifest.toml")
}

type manifestNode struct{ Name, Role, Config string }

type manifestT struct {
	Node     []manifestNode
	Topology struct {
		Layer []struct{ Mixes []string }
	}
}

func Layers(manifest string) ([][]string, error) {
	var m manifestT
	if _, err := toml.DecodeFile(manifest, &m); err != nil {
		return nil, err
	}
	out := make([][]string, 0, len(m.Topology.Layer))
	for _, l := range m.Topology.Layer {
		out = append(out, l.Mixes)
	}
	return out, nil
}

func Mixes(repo, network string) ([]string, error) {
	nodes, err := loadNodes(repo, network)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, n := range nodes {
		if n.Role == "mix" && n.Name != "" {
			out = append(out, n.Name)
		}
	}
	sort.Strings(out)
	return out, nil
}

func Orphans(repo, network string) ([]string, error) {
	mixes, err := Mixes(repo, network)
	if err != nil {
		return nil, err
	}
	layers, err := Layers(ManifestPath(repo, network))
	if err != nil {
		return nil, err
	}
	placed := map[string]bool{}
	for _, l := range layers {
		for _, m := range l {
			placed[m] = true
		}
	}
	var out []string
	for _, m := range mixes {
		if !placed[m] {
			out = append(out, m)
		}
	}
	return out, nil
}

func emptiest(layers [][]string) int {
	best, n := 0, -1
	for i, l := range layers {
		if n < 0 || len(l) < n {
			best, n = i, len(l)
		}
	}
	return best
}

func insert(text string, layer int, mix string) (string, error) {
	lines := strings.Split(text, "\n")
	cur := -1
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "[[topology.layer]]" {
			cur++
			continue
		}
		if cur != layer || !strings.HasPrefix(t, "mixes") {
			continue
		}
		eq := strings.Index(ln, "=")
		open := strings.Index(ln, "[")
		close := strings.LastIndex(ln, "]")
		if eq < 0 || open < eq || close <= open {
			return "", fmt.Errorf("layer %d: cannot read its mixes line: %s", layer, ln)
		}
		body := strings.TrimSpace(ln[open+1 : close])
		entry := fmt.Sprintf("%q", mix)
		if body != "" {
			body += ", "
		}
		lines[i] = ln[:open+1] + body + entry + ln[close:]
		return strings.Join(lines, "\n"), nil
	}
	return "", fmt.Errorf("layer %d has no mixes line in the manifest", layer)
}

func diff(a, b, path string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s (proposed)\n", path, path)
	if len(al) != len(bl) {
		fmt.Fprintf(&out, "@@ the proposal changed the line count (%d -> %d) @@\n", len(al), len(bl))
	}
	n := len(al)
	if len(bl) < n {
		n = len(bl)
	}
	for i := 0; i < n; i++ {
		if al[i] == bl[i] {
			continue
		}
		fmt.Fprintf(&out, "@@ line %d @@\n-%s\n+%s\n", i+1, al[i], bl[i])
	}
	return out.String()
}

func Propose(o Options) (Proposal, error) {
	path := ManifestPath(o.Repo, o.Network)
	raw, err := os.ReadFile(path)
	if err != nil {
		return Proposal{}, err
	}
	orphans, err := Orphans(o.Repo, o.Network)
	if err != nil {
		return Proposal{}, err
	}
	if len(orphans) == 0 {
		return Proposal{}, nil
	}
	layers, err := Layers(path)
	if err != nil {
		return Proposal{}, err
	}
	if len(layers) == 0 {
		return Proposal{}, fmt.Errorf("%s declares no topology layers", path)
	}

	owners, err := mixOperators(o.Repo, o.Network)
	if err != nil {
		return Proposal{}, err
	}
	text := string(raw)
	p := Proposal{}
	r := bufio.NewReader(o.in())
	for _, mix := range orphans {
		operator := owners[mix]
		occupied := operatorLayers(owners, layers, operator)
		layer := preferredLayer(layers, occupied)
		if operator != "" {
			fmt.Fprintf(o.log(), "  operator %s is currently in mix layers %v\n", operator, occupied)
		}
		if o.Ask {
			layer, err = ask(r, o.log(), mix, layers, layer)
			if err != nil {
				return Proposal{}, err
			}
		}
		if len(layers) > 1 && len(occupied) == len(layers)-1 && !slices.Contains(occupied, layer) {
			return Proposal{}, fmt.Errorf("placing %s in layer %d would give operator %s visibility in every mix layer", mix, layer, operator)
		}
		text, err = insert(text, layer, mix)
		if err != nil {
			return Proposal{}, err
		}

		layers[layer] = append(layers[layer], mix)
		p.Placements = append(p.Placements, Placement{Mix: mix, Layer: layer})
		fmt.Fprintf(o.log(), "  %s -> layer %d\n", mix, layer)
	}
	p.Manifest = text
	p.Diff = diff(string(raw), text, path)
	return p, nil
}

func ask(r *bufio.Reader, w io.Writer, mix string, layers [][]string, def int) (int, error) {
	for i, l := range layers {
		fmt.Fprintf(w, "  layer %d has %d mix(es): %s\n", i, len(l), strings.Join(l, " "))
	}
	fmt.Fprintf(w, "  layer for %s [%d]: ", mix, def)
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	for i := range layers {
		if line == fmt.Sprintf("%d", i) {
			return i, nil
		}
	}
	return 0, fmt.Errorf("invalid layer %q. This network has %d", line, len(layers))
}

func Write(p Proposal, path string) error {
	if p.Manifest == "" {
		return fmt.Errorf("nothing to write")
	}
	return os.WriteFile(path, []byte(p.Manifest), 0o644)
}
