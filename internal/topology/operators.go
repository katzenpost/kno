package topology

import (
	"strings"

	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/nodefile"
)

func mixOperators(repo, network string) (map[string]string, error) {
	machines, err := nodefile.Machines(knopath.EditableDir(repo, network))
	if err != nil {
		return nil, err
	}
	owners := map[string]string{}
	for name, machine := range machines {
		if machine.Operator == "" {
			continue
		}
		owners[name] = machine.Operator
	}
	for _, machine := range machines {
		for _, svc := range machine.Services {
			if mix, ok := strings.CutSuffix(svc, "-pq-mixserver"); ok {
				owners[mix] = machine.Operator
			}
		}
	}
	return owners, nil
}

func operatorLayers(owners map[string]string, layers [][]string, operator string) []int {
	var out []int
	if operator == "" {
		return out
	}
	for i, layer := range layers {
		for _, mix := range layer {
			if owners[mix] == operator {
				out = append(out, i)
				break
			}
		}
	}
	return out
}

func preferredLayer(layers [][]string, occupied []int) int {
	if len(occupied) == 0 {
		return emptiest(layers)
	}
	best := occupied[0]
	for _, i := range occupied[1:] {
		if len(layers[i]) < len(layers[best]) {
			best = i
		}
	}
	return best
}
