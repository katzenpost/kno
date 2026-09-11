package doctor

import (
	"os/exec"
	"strings"
)

func packaged(path string) bool {
	return strings.HasPrefix(path, "/usr/lib/systemd/") || strings.HasPrefix(path, "/lib/systemd/")
}

func masked(unit string) bool { return showUnit(unit, "LoadState") == "masked" }

func shadowed(unit string) string {
	if masked(unit) || !packagedUnit(unit) {
		return ""
	}
	p := showUnit(unit, "FragmentPath")
	if p == "" || packaged(p) || ownedByAPackage(p) {
		return ""
	}
	return p
}

func ownedByAPackage(path string) bool {
	if !haveDpkg() {
		return true
	}
	return exec.Command("dpkg-query", "-S", path).Run() == nil
}

func reportShadowed(r *Result, unit, path string) {
	r.warn(unit, "systemd runs it from %s, not from the packaged unit", path)
	r.line("       That file has priority over the file that the package installs, so")
	r.line("       changes that kno makes to this unit do not reach this host.")
	r.line("       If you put the file there, it is your override. If you did not,")
	r.line("       nobody maintains it.")
	r.line("       Read the file first. It may differ from the packaged file in a way")
	r.line("       that this host needs. Run this on %s:", r.label)
	r.line("           diff %s /lib/systemd/system/%s", path, unitFile(unit))
	r.line("       To give the unit back to the package and keep that file:")
	r.line("       On the organizer, run:")
	r.line("           kno doctor --host %s --fix", r.label)
	r.line("       On %s, run:", r.label)
	r.line("           sudo kno doctor --fix")
	r.line("       --fix adds %s to the name of that file and restarts the unit.", supersededSuffix)
}

func unitFile(unit string) string {
	if at := strings.Index(unit, "@"); at > 0 {
		return unit[:at+1] + ".service"
	}
	return unit + ".service"
}
