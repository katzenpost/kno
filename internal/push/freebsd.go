package push

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/katzenpost/kno/internal/build"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/pkg/freebsd"
	"github.com/katzenpost/kno/internal/sign"
	"github.com/katzenpost/kno/internal/sshx"
)

func planInstallFreeBSD(o Options, p Plan) (Plan, error) {
	pkgs := filepath.Join(o.Rel, "pkgs")
	names := []string{"katzenpost", configPkgPrefix + freebsd.ClientService}
	for _, svc := range o.Services {
		names = append(names, configPkgPrefix+svc)
	}
	names = append(names, "kno", freebsd.HostConfigName(o.Label))
	for _, n := range names {
		f, err := freebsd.OnePkg(pkgs, n)
		if err != nil {
			return Plan{}, fmt.Errorf("%w. Run: kno build --host %s", err, o.Label)
		}
		p.Packages = append(p.Packages, f)
	}
	kno := p.Packages[len(p.Packages)-2]
	if want := o.KnoVersion; want != "" && filepath.Base(kno) != "kno-"+freebsd.Version(want)+".pkg" {
		return Plan{}, fmt.Errorf("this release needs kno %s and the release holds %s"+
			" (build it: kno build --host %s)", freebsd.Version(want), filepath.Base(kno), o.Label)
	}
	var manifests []string
	for _, svc := range o.Services {
		m := filepath.Join(o.Rel, "manifest", svc+".conf")
		if !exists(m) {
			return Plan{}, fmt.Errorf("missing %s (build it: kno build --host %s)", m, o.Label)
		}
		manifests = append(manifests, m)
	}

	remote := RemoteRelease(o.Tag)
	p.Steps = append(p.Steps, Step{Argv: sshx.SSH(o.SSH, o.Node, "mkdir -p "+remote+"/pkgs "+remote+"/manifest")})
	p.Steps = append(p.Steps, transfer(o, pkgs, append(slices.Clone(p.Packages), sumsFiles(pkgs)...),
		remote+"/pkgs", false)...)
	p.Steps = append(p.Steps, transfer(o, filepath.Join(o.Rel, "manifest"), manifests, remote+"/manifest", false)...)
	if o.Mode == ModeCopy {
		return p, nil
	}
	p.Steps = append(p.Steps,
		Step{Argv: sshx.SSH(o.SSH, o.Node, freebsdBootstrap(remote, baseNames(p.Packages), filepath.Base(kno),
			filepath.Base(p.Packages[len(p.Packages)-1]))),
			Note: "installed kno on " + o.Node},
		Step{Argv: sshx.SSH(o.SSH, o.Node, sshx.AsRoot(knopath.LocalBin+" activate "+remote+" "+o.Tag))},
	)
	return p, nil
}

func freebsdBootstrap(remote string, names []string, kno, hostConfig string) string {
	sums := build.SumsName
	return "set -e; cd " + remote + "/pkgs; " +
		"[ -f " + sums + " ] || { echo 'kno: " + sums + " is not with the copied packages. kno does not install them.' >&2; exit 1; }; " +
		"for p in " + strings.Join(names, " ") + "; do " +
		`want=$(awk -v f="$p" '$2 == f { print $1 }' ` + sums + "); " +
		`[ -n "$want" ] || { echo "kno: $p is not in ` + sums + `. kno does not install it" >&2; exit 1; }; ` +
		`[ "$(sha256 -q "$p")" = "$want" ] || { echo "kno: $p does not match ` + sums + `" >&2; exit 1; }; done; ` +
		sigCheck(sums, sign.NodePathFor("freebsd")) +
		sshx.AsRoot(strings.Join(freebsd.PkgAdd("", kno, hostConfig), " ")) + "; " +
		"[ -x " + knopath.LocalBin + " ] || { echo 'kno: installed the package but there is no " + knopath.LocalBin + "' >&2; exit 1; }"
}
