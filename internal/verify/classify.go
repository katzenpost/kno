package verify

import (
	"fmt"
)

const (
	CodeHealthy = 0

	CodeNotYet = 1
	CodeFailed = 2

	CodeNoConsensus = 3

	CodeCannotAsk = 4

	DefaultMaxRestarts = 3
)

func AboutTheNode(code int) bool {
	switch code {
	case CodeNoConsensus, CodeCannotAsk:
		return false
	}
	return true
}

type UnitState struct {
	Unit     string
	Failed   bool
	Active   string
	Restarts int

	Enabled bool
}

func ClassifyUnit(u UnitState, maxRestarts int) (int, string) {
	if u.Failed {
		return CodeFailed, u.Unit + " is failed"
	}
	if u.Restarts >= 0 && u.Restarts > maxRestarts {
		return CodeFailed, fmt.Sprintf("%s restarted %d times. The limit is %d.",
			u.Unit, u.Restarts, maxRestarts)
	}
	switch u.Active {
	case "", "active":
	case "activating", "deactivating", "reloading":
		return CodeNotYet, u.Unit + " is " + u.Active + " (transient)"
	default:
		return CodeFailed, u.Unit + " is " + u.Active
	}
	return CodeHealthy, ""
}

type NodeIdentifier struct {
	Where string
	ID    string
}

func Participating(out string, ids []NodeIdentifier) (NodeIdentifier, bool) {
	d, ok := ParseDoc(out)
	if !ok {
		if len(ids) > 0 {
			return ids[0], false
		}
		return NodeIdentifier{}, false
	}
	nodes, signers := map[string]bool{}, map[string]bool{}
	for _, n := range d.Nodes() {
		nodes[n] = true
	}
	for _, s := range d.Signers {
		signers[s] = true
	}
	for _, n := range ids {
		var in bool
		switch n.Where {
		case "authority":
			in = signers[n.ID]
		case "any":
			in = signers[n.ID] || nodes[n.ID]
		default:
			in = nodes[n.ID]
		}
		if n.ID == "" || !in {
			return n, false
		}
	}
	return NodeIdentifier{}, true
}
