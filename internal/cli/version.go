package cli

import (
	"runtime/debug"
	"strings"
)

var stamped string

func Version() string {
	if stamped != "" {
		return stamped
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	v := info.Main.Version
	if v == "" {
		v = "(devel)"
	}
	rev, dirty := "", false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev != "" {
		if len(rev) > 12 {
			rev = rev[:12]
		}

		if !strings.Contains(v, rev) {
			v += " (" + rev + ")"
		}
	}

	if dirty && !strings.Contains(v, "dirty") {
		v += " dirty"
	}
	return v
}
