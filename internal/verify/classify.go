package verify

import (
	"fmt"
	"regexp"
	"strings"
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

func Participating(dump string, ids []NodeIdentifier) (NodeIdentifier, bool) {
	body, signers := splitSignedBy(dump)
	for _, n := range ids {
		hay := body
		switch n.Where {
		case "authority":
			hay = signers
		case "any":
			hay = dump
		}
		if !wordMatch(hay, n.ID) {
			return n, false
		}
	}
	return NodeIdentifier{}, true
}

func splitSignedBy(dump string) (body, signers string) {
	lines := strings.Split(dump, "\n")
	for i, ln := range lines {
		if strings.Contains(ln, "signed by") {
			return strings.Join(lines[:i], "\n"), strings.Join(lines[i:], "\n")
		}
	}
	return dump, ""
}

func wordMatch(hay, id string) bool {
	if id == "" {
		return false
	}
	re, err := regexp.Compile(`\b` + regexp.QuoteMeta(id) + `\b`)
	if err != nil {
		return false
	}
	return re.MatchString(hay)
}
