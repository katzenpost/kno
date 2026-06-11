package nodefile

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

const MachinesDir = "machines"

type Machine struct {
	Operator string `toml:"operator"`
	Arch     string `toml:"arch"`
	OS       string `toml:"os"`
	LAN      string `toml:"lan"`
}

func Machines(editable string) (map[string]Machine, error) {
	dir := filepath.Join(editable, MachinesDir)
	ents, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]Machine{}
	for _, e := range ents {
		name, ok := strings.CutSuffix(e.Name(), ".toml")
		if e.IsDir() || !ok {
			continue
		}
		var m Machine
		md, err := toml.DecodeFile(filepath.Join(dir, e.Name()), &m)
		if err != nil {
			return nil, fmt.Errorf("%s/%s: %w", MachinesDir, e.Name(), err)
		}
		if extra := md.Undecoded(); len(extra) > 0 {
			return nil, fmt.Errorf("%s/%s: unknown key %s; a machine file has operator,"+
				" arch, os and lan", MachinesDir, e.Name(), extra[0])
		}
		out[name] = m
	}
	return out, nil
}

func Operators(ms map[string]Machine) map[string][]string {
	out := map[string][]string{}
	for name, m := range ms {
		if m.Operator != "" {
			out[m.Operator] = append(out[m.Operator], name)
		}
	}
	for _, nodes := range out {
		sort.Strings(nodes)
	}
	return out
}

func LANGroups(ms map[string]Machine) [][]string {
	byGroup := map[string][]string{}
	for name, m := range ms {
		if m.LAN != "" {
			byGroup[m.LAN] = append(byGroup[m.LAN], name)
		}
	}
	names := make([]string, 0, len(byGroup))
	for g := range byGroup {
		names = append(names, g)
	}
	sort.Strings(names)
	out := make([][]string, 0, len(names))
	for _, g := range names {
		sort.Strings(byGroup[g])
		out = append(out, byGroup[g])
	}
	return out
}
