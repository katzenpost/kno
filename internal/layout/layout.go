package layout

import (
	"strings"

	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/service"
)

const Since = "0.0.107"

func Legacy(version string) bool {
	v := strings.TrimPrefix(version, "v")
	return v != "" && deb.CompareVersions(v, Since) < 0
}

func Unit(s service.Service, version string) string {
	if s.LegacyUnit != "" && Legacy(version) {
		return s.LegacyUnit
	}
	return s.Unit
}

func StateDir(s service.Service, version string) string {
	if s.LegacyStateDir != "" && Legacy(version) {
		return s.LegacyStateDir
	}
	return s.StateDir
}
