package doctor

import (
	"os/exec"
	"strings"

	"github.com/katzenpost/kno/internal/pkg/deb"
)

var dpkg = func(args ...string) string {
	out, _ := exec.Command(args[0], args[1:]...).Output()
	return string(out)
}

func ourPackages(list string) []string {
	var out []string
	for _, line := range strings.Split(list, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[1] == "installed" && deb.InScope(f[0]) {
			out = append(out, f[0])
		}
	}
	return out
}

func reportConffiles(r *Result) {
	if _, err := exec.LookPath("dpkg"); err != nil {
		return
	}
	var missing []string
	for _, pkg := range ourPackages(dpkg("dpkg-query", "-W", "-f", "${Package} ${db:Status-Status}\n")) {
		missing = append(missing, missingConffiles(pkg)...)
	}
	if len(missing) == 0 {
		return
	}
	for _, p := range missing {
		r.check("config missing", false, "%s is shipped by a kno package and is not on disk", p)
	}
	r.line("       A conffile removed by hand stays removed: dpkg records the removal")
	r.line("       as deliberate, so every later install leaves it missing while")
	r.line("       'dpkg -L' still lists it. Nothing reports it except whatever tried")
	r.line("       to read the file.")
	r.line("       kno update installs the packages with dpkg --force-confmiss, which")
	r.line("       restores the file.")
	reportUpdate(r, true)
	r.line("       Or run this on %s:", r.label)
	r.line("           sudo dpkg -i --force-confmiss <the package>")
}

func missingConffiles(pkg string) []string {
	var found []string
	for _, line := range strings.Split(dpkg("dpkg", "-V", pkg), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || !strings.HasPrefix(f[0], "missing") {
			continue
		}

		if f[1] != "c" {
			continue
		}
		found = append(found, f[2])
	}
	return found
}
