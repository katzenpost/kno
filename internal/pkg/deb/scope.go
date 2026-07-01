package deb

import "strings"

var OurPackages = []string{
	"katzenpost",
	"katzenpost-warped",
	"kno",
	"kno-host-config",
	"kno-host-config-*",
	"namenlos-config-*",
}

func InScope(name string) bool {
	if name == "" {
		return false
	}
	for _, a := range OurPackages {
		if pre, ok := strings.CutSuffix(a, "*"); ok {
			if strings.HasPrefix(name, pre) {
				return true
			}
			continue
		}
		if name == a {
			return true
		}
	}
	return false
}
