package build

import (
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/knopath"
)

func UnclaimedInRecord(recorded []PackageSum, services []string) []string {
	if len(services) == 0 {
		return nil
	}
	claimed := make(map[string]bool, len(services))
	for _, s := range services {
		claimed[s] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range recorded {
		svc, ok := configPkgService(p.Name)
		if !ok || claimed[svc] || seen[svc] {
			continue
		}
		seen[svc] = true
		out = append(out, svc)
	}
	sort.Strings(out)
	return out
}

func configPkgService(name string) (string, bool) {
	if !strings.HasPrefix(name, knopath.ConfigPkgPrefix) {
		return "", false
	}
	rest := strings.TrimPrefix(name, knopath.ConfigPkgPrefix)
	svc, _, ok := strings.Cut(rest, "_")
	if !ok || svc == "" {
		return "", false
	}
	return strings.TrimSuffix(svc, "-warped"), true
}
