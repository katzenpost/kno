package probe

import "strings"

type Status string

const (
	StatusOK          Status = "OK"
	StatusUp          Status = "UP"
	StatusDown        Status = "DOWN"
	StatusOrphan      Status = "ORPHAN"
	StatusKnownOrphan Status = "ORPHAN(known)"
	StatusGhost       Status = "GHOST"
	StatusNotSigning  Status = "NOT-SIGNING"
	StatusUnknown     Status = "UNKNOWN-IN-CONSENSUS"
)

const RoleAuthority = "authority"

func Mismatch(consensusAddr string) Status { return Status("MISMATCH(" + consensusAddr + ")") }

type Endpoint struct {
	Role       string
	Identifier string
	Host       string
	Port       string
}

func (e Endpoint) Addr() string { return e.Host + ":" + e.Port }

type ConsensusNode struct {
	Identifier string
	Host       string
	Port       string
}

func (n ConsensusNode) Addr() string { return n.Host + ":" + n.Port }

type Consensus struct {
	Nodes   []ConsensusNode
	Signers []string
}

func (c Consensus) Addr(id string) string {
	for _, n := range c.Nodes {
		if n.Identifier == id {
			return n.Addr()
		}
	}
	return ""
}

func (c Consensus) Signed(id string) bool {
	for _, s := range c.Signers {
		if s == id {
			return true
		}
	}
	return false
}

type Observation struct {
	Role          string
	Up            bool
	ConsensusOK   bool
	ConfigAddr    string
	ConsensusAddr string
	Signing       bool
	KnownOrphan   bool
}

type Verdict struct {
	Status      Status
	Discrepancy bool
}

func Classify(o Observation) Verdict {
	if o.Role == RoleAuthority {
		switch {
		case o.Up && (o.Signing || !o.ConsensusOK):
			return Verdict{StatusOK, false}
		case !o.Up:
			return Verdict{StatusDown, true}
		default:
			return Verdict{StatusNotSigning, true}
		}
	}
	if !o.ConsensusOK {
		if o.Up {
			return Verdict{StatusUp, false}
		}
		return Verdict{StatusDown, true}
	}
	inConsensus := o.ConsensusAddr != ""
	switch {
	case o.Up && inConsensus && o.ConsensusAddr == o.ConfigAddr:
		return Verdict{StatusOK, false}
	case o.Up && inConsensus:
		return Verdict{Mismatch(o.ConsensusAddr), true}
	case o.Up:
		if o.KnownOrphan {
			return Verdict{StatusKnownOrphan, false}
		}
		return Verdict{StatusOrphan, true}
	case inConsensus:
		return Verdict{StatusGhost, true}
	}
	return Verdict{StatusDown, true}
}

func UnknownInConsensus(nodes []ConsensusNode, eps []Endpoint) []ConsensusNode {
	known := make(map[string]bool, len(eps))
	for _, e := range eps {
		known[e.Identifier] = true
	}
	var out []ConsensusNode
	for _, n := range nodes {
		if !known[n.Identifier] {
			out = append(out, n)
		}
	}
	return out
}

func KnownOrphans(list string) []string { return strings.Fields(list) }

func IsKnownOrphan(known []string, id string) bool {
	for _, k := range known {
		if k == id {
			return true
		}
	}
	return false
}

func RoleOf(base string) string {
	switch {
	case strings.HasSuffix(base, "-pq-authority"):
		return RoleAuthority
	case strings.HasSuffix(base, "-pq-mixserver"):
		return "mix"
	case strings.HasSuffix(base, "-pq-gatewaynode"):
		return "gateway"
	case strings.HasSuffix(base, "-pq-servicenode"):
		return "service"
	case strings.Contains(base, "-pq-storagenode"):
		return "replica"
	}
	return "node"
}
