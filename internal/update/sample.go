package update

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/katzenpost/kno/internal/layout"
	"github.com/katzenpost/kno/internal/record"
	"github.com/katzenpost/kno/internal/service"
	"github.com/katzenpost/kno/internal/track"
	"github.com/katzenpost/kno/internal/verify"
)

func (r *RemoteRunner) sample(note string) *record.Sample {
	now := time.Now().UTC()
	s := &record.Sample{Sampled: now, Note: note,
		Consensus: r.consensus(), Verified: verdictOf(r.verified)}
	if s.Consensus != "" || s.Verified != "" {
		s.VerifiedAt = now
	}
	units := make([]string, 0, len(r.Host.Services)+1)
	for _, svc := range r.Host.Services {
		unit := svc
		if rs, err := service.ResolveService(r.Env.Repo, r.Env.Network, svc); err == nil && rs.Unit != "" {
			unit = layout.Unit(rs, r.Env.CurrentVersion())
		}
		units = append(units, unit)
	}

	if r.Env.Config.RunsKpclientd(r.Host.Label) {
		units = append(units, service.ClientUnit)
	}
	out, code, err := r.query(sampleCmd(units))
	if err != nil || code == sshTransportFailure {
		s.Note = strings.TrimSpace(note + " (host did not answer)")
		return s
	}
	s.Packages, s.Units = parseSample(out, units)
	return s
}

func sampleCmd(units []string) string {
	cmd := `dpkg-query -W -f '${Package} ${Version}\n' katzenpost kno 'namenlos-config-*' 2>/dev/null; echo '#units'`
	if len(units) > 0 {
		cmd += "; systemctl is-active " + strings.Join(units, " ") + " 2>/dev/null"
	}
	return cmd
}

func parseSample(out string, units []string) (map[string]string, map[string]string) {
	pkgs, states := map[string]string{}, map[string]string{}
	head, tail, _ := strings.Cut(out, "#units")
	for _, line := range strings.Split(head, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 {
			pkgs[f[0]] = f[1]
		}
	}
	lines := strings.Fields(tail)
	for i, u := range units {
		if i < len(lines) && isUnitState(lines[i]) {
			states[u] = lines[i]
		}
	}
	if len(pkgs) == 0 {
		pkgs = nil
	}
	if len(states) == 0 {
		states = nil
	}
	return pkgs, states
}

func isUnitState(s string) bool {
	switch s {
	case "active", "inactive", "failed", "activating", "deactivating", "reloading", "unknown":
		return true
	}
	return false
}

func (r *RemoteRunner) consensus() string {
	switch {
	case r.verified == nil:
		return ""
	case *r.verified == 0:
		return "in"
	case *r.verified == VerifyUnavailable:
		return "unknown"
	case *r.verified == verify.CodeNoConsensus:
		return "none on the network"
	case *r.verified == verify.CodeCannotAsk:
		return "could not ask"
	default:
		return "not yet"
	}
}

func verdictOf(code *int) string {
	if code == nil {
		return ""
	}
	switch *code {
	case 0:
		return record.VerifiedOK
	case VerifyUnavailable:
		return "verify could not run"
	case verify.CodeFailed:
		return record.VerifiedFailed
	case verify.CodeNoConsensus:
		return "no consensus on the network"
	case verify.CodeCannotAsk:
		return "this host could not ask"
	}
	return "not yet"
}

func keepBadRecord(l track.Logger, e, he track.Env, label string) {
	_, err := record.LoadPending(he.StateDir, e.Repo, e.Network, label)
	if !errors.Is(err, record.ErrMalformed) {
		return
	}
	if p := record.PendingPath(he.StateDir); he.StateDir != "" {
		if os.Rename(p, p+".bad") == nil {
			l.Warnf("kno cannot read the deployment record (%v). kno keeps"+
				" it as %s.bad and starts a new record", err, p)
			return
		}
	}
	l.Warnf("kno cannot read the deployment record (%v). kno starts a"+
		" new record, and git keeps the earlier one", err)
}

func recordFor(e track.Env, he track.Env, h remoteHost, target string,
	before, after *record.Sample, by string) record.Record {
	r, err := record.LoadPending(he.StateDir, e.Repo, e.Network, h.Label)
	if err != nil {
		r = record.Record{Host: h.Label}
	}
	r.Host, r.Node, r.Services = h.Label, h.Node, h.Services
	if r.Observed == nil || r.Deploying == nil {

		r.Observed = before
	}

	if samePackages(before, after) {
		before = nil
		r.Observed = nil
	}

	state := readLine(filepath.Join(he.StateDir, "state"))
	current := he.CurrentVersion()
	kv, _ := readKV(filepath.Join(he.StateDir, "pending"))

	switch {
	case state == "pending":
		d := &record.Deploying{
			Started: time.Now().UTC(),
			Tag:     kv["tag"],
			From:    kv["prev"],
			By:      by,
			Held:    kv["held"] == "1",
		}
		if r.Deploying != nil && r.Deploying.Tag == d.Tag {
			d.Started, d.Packages = r.Deploying.Started, r.Deploying.Packages
		}
		if after != nil && len(after.Packages) > 0 {
			d.Packages = after.Packages
		}
		if secs := kv["deadline"]; secs != "" {
			if n, err := strconv.ParseInt(secs, 10, 64); err == nil {
				d.Deadline = time.Unix(n, 0).UTC()
			}
		}
		r.Deploying = d
		r.Deployed = nil

	case current == target && current != "":

		if r.Succeeded == nil || r.Succeeded.Tag != current {
			from := ""
			if r.Deploying != nil {
				from = r.Deploying.From
			}
			r.Succeeded = &record.Outcome{
				Finished: time.Now().UTC(), Tag: current, From: from, By: by,
				Verified: verifiedOf(after),
			}
			r.RolledBack = nil
		}
		r.Deploying = nil
		r.Deployed = carryVerification(after, r.Deployed)

	case r.Deploying != nil && he.Quarantined(r.Deploying.Tag):
		why := "did not pass verify inside the verify window"
		if verifiedOf(after) == record.VerifiedFailed {
			why = "verify failed (exit 2)"
		}
		r.RolledBack = &record.Outcome{
			Finished: time.Now().UTC(),
			Tag:      r.Deploying.Tag,
			From:     r.Deploying.From,
			By:       by,
			Why:      why,
		}
		r.Succeeded = nil
		r.Deploying = nil
		r.Deployed = carryVerification(after, r.Deployed)

	default:

		r.Deploying = nil
		if after != nil {
			r.Observed = carryVerification(after, prior(r.Observed, r.Deployed))
		}
	}
	return r
}

func carryVerification(next, prev *record.Sample) *record.Sample {
	if next == nil || prev == nil {
		return next
	}
	if next.Consensus == "" && next.Verified == "" {
		next.Consensus, next.Verified = prev.Consensus, prev.Verified
		next.VerifiedAt = prev.VerifiedAt
	}
	return next
}

func prior(observed, deployed *record.Sample) *record.Sample {
	if observed != nil && (observed.Verified != "" || observed.Consensus != "") {
		return observed
	}
	return deployed
}

func thisMachine() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

func samePackages(a, b *record.Sample) bool {
	if a == nil || b == nil {
		return false
	}
	if len(a.Packages) != len(b.Packages) || len(a.Packages) == 0 {
		return false
	}
	for name, ver := range a.Packages {
		if b.Packages[name] != ver {
			return false
		}
	}
	return true
}

func verifiedOf(s *record.Sample) string {
	if s == nil {
		return ""
	}
	return s.Verified
}
