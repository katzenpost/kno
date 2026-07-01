package deb

import (
	"strings"
	"testing"
)

func TestGeneratedRelationshipsStayInOurNamespace(t *testing.T) {
	for _, base := range []string{"katzenpost", "namenlos-config-annares-pq-authority"} {
		for _, warped := range []bool{false, true} {
			name := Flavoured(base, warped)
			fields := AlternativesFields(base, "0.0.104-1")
			for _, named := range relationshipTargets(fields) {
				if !inFamily(named, base) {
					t.Errorf("%s declares a relationship on %q, which is outside our namespace",
						name, named)
				}
			}
		}
	}
}

func relationshipTargets(control string) []string {
	var out []string
	for _, line := range strings.Split(control, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "Provides", "Replaces", "Conflicts", "Breaks":
		default:
			continue
		}
		for _, item := range strings.Split(v, ",") {
			if n := strings.TrimSpace(strings.Split(strings.TrimSpace(item), " ")[0]); n != "" {
				out = append(out, n)
			}
		}
	}
	return out
}

func inFamily(name, base string) bool {
	return InScope(name) ||
		name == Virtual(base) ||
		name == Virtual(Flavoured(base, true))
}

func TestPerHostConfigOnlyClaimsTheGenericPackage(t *testing.T) {
	for _, named := range relationshipTargets(
		"Provides: kno-host-config\nConflicts: kno-host-config\nReplaces: kno-host-config\n") {
		if !InScope(named) {
			t.Errorf("the per-host config package claims %q, which is outside our namespace", named)
		}
	}
}
