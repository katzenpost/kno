package check

import (
	"fmt"

	"github.com/katzenpost/kno/internal/topology"
)

type TopologyOptions struct {
	Repo string

	Network string

	Known []string
}

func Topology(o TopologyOptions) (Result, error) {
	var r Result
	if o.Repo == "" {
		return r, fmt.Errorf("topology check: no repo given")
	}
	known := o.Known
	if known == nil {
		var err error
		known, err = topology.KnownFindings(o.Repo)
		if err != nil {
			r.add("topology", "known findings", Warn,
				"cannot read the allowlist (%v); every finding counts as new", err)
		}
	}

	nets := []string{o.Network}
	if o.Network == "" {
		var err error
		nets, err = networksWithManifest(o.Repo)
		if err != nil {
			return r, err
		}
		if len(nets) == 0 {
			r.add("topology", "networks", Fail,
				"no networks/<name>/editable-configs/manifest.toml under %s; nothing was linted", o.Repo)
			return r, nil
		}
	}

	seen := map[string]bool{}
	for _, n := range nets {
		found, err := topology.Lint(o.Repo, n)
		if err != nil {

			r.add("topology", n, Fail, "not linted: %v", err)
			continue
		}

		for _, f := range found {
			if topology.Known(known, f) {
				seen[f.Subject] = true
				r.add("topology", n+" "+f.Subject, Warn, "known gap: %s", f.Detail)
				continue
			}
			r.add("topology", n+" "+f.Subject, Fail,
				"%s; fix the network data, or add the line %q to data/topology-findings.txt",
				f.Detail, f.Subject)
		}
		if len(found) == 0 {
			r.add("topology", n, Pass, "consistent")
		}
	}

	if o.Network != "" {
		return r, nil
	}
	for _, k := range known {
		covered := false
		for subject := range seen {
			if topology.Known([]string{k}, topology.Finding{Subject: subject}) {
				covered = true
				break
			}
		}
		if !covered {
			r.add("topology", "resolved "+k, Pass,
				"no network reports it any more; drop it from data/topology-findings.txt")
		}
	}
	return r, nil
}
