package doctor

import (
	"os/exec"
	"strings"

	"github.com/katzenpost/kno/internal/knopath"
)

type pkgState struct {
	Name      string
	Installed bool
	Version   string
}

func dpkgStatus(name string) pkgState {
	out, err := exec.Command("dpkg-query", "-W", "-f", "${Status} ${Version}", name).Output()
	if err != nil {
		return pkgState{Name: name}
	}
	f := strings.Fields(string(out))

	if len(f) >= 4 && f[2] == "installed" {
		return pkgState{Name: name, Installed: true, Version: f[3]}
	}
	return pkgState{Name: name}
}

func haveDpkg() bool {
	_, err := exec.LookPath("dpkg-query")
	return err == nil
}

type provenance struct {
	Service string
	Binary  pkgState
	Config  pkgState

	Checked bool
}

func (p provenance) Managed() bool { return p.Binary.Installed && p.Config.Installed }

func gatherProvenance(svc string) provenance {
	p := provenance{Service: svc}
	if !haveDpkg() {
		return p
	}
	p.Checked = true
	p.Binary = dpkgStatus("katzenpost")
	p.Config = dpkgStatus(configPkgPrefix + svc)
	return p
}

const configPkgPrefix = knopath.ConfigPkgPrefix

func reportProvenance(r *Result, p provenance) bool {
	if !p.Checked || p.Managed() {
		return false
	}
	var missing []string
	if !p.Binary.Installed {
		missing = append(missing, "katzenpost (the binaries)")
	}
	if !p.Config.Installed {
		missing = append(missing, configPkgPrefix+p.Service+" (the config, unit, service account and public keys)")
	}
	r.check(p.Service, false, "this service was not installed by kno: %s %s not installed",
		strings.Join(missing, " and "), isAre(len(missing)))
	r.line("       A person or an older process installed what runs here, so a fault")
	r.line("       below about accounts, permissions or keys is a result of that, not")
	r.line("       a separate fault. To install the packages, which is the full repair:")
	reportUpdate(r, true)
	r.line("       The packages contain no private key. kno does not install a package")
	r.line("       that contains a private key.")
	return true
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}
