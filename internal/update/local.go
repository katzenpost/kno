package update

import (
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/pkg/freebsd"
	"github.com/katzenpost/kno/internal/push"
	"github.com/katzenpost/kno/internal/record"
	"github.com/katzenpost/kno/internal/service"
	"github.com/katzenpost/kno/internal/track"
	"github.com/katzenpost/kno/internal/verify"
)

func localSample(e track.Env, note, consensus string) *record.Sample {
	if len(e.Services) == 0 {
		return nil
	}
	s := &record.Sample{Sampled: time.Now().UTC(), Note: note, Consensus: consensus}
	units := make([]string, 0, len(e.Services))
	for _, svc := range e.Services {
		unit := svc
		if r, err := service.ResolveService(e.Repo, e.Network, svc); err == nil && r.Unit != "" {
			unit = verify.ServiceUnit(r)
		}
		units = append(units, unit)
	}
	if runtime.GOOS == "freebsd" {
		s.Packages = jailPackages()
	} else {
		s.Packages = localPackages()
	}
	s.Units = localUnits(units)
	return s
}

func jailPackages() map[string]string {
	pkgs := map[string]string{}
	names := []string{"query", "-g", "%n %v", "katzenpost", "kno*", "namenlos-config-*"}
	parse := func(prefix string, out []byte) {
		for _, line := range strings.Split(string(out), "\n") {
			if f := strings.Fields(line); len(f) == 2 {
				pkgs[prefix+f[0]] = f[1]
			}
		}
	}
	query := func(prefix string, args ...string) {
		out, _ := exec.Command("pkg", append(args, names...)...).Output()
		parse(prefix, out)
	}
	query("")
	if dirs, err := os.ReadDir(freebsd.RootsDir); err == nil {
		for _, d := range dirs {
			argv := freebsd.PkgQueryInRoot(freebsd.CurrentRoot(d.Name()), names[1:]...)
			out, _ := exec.Command("sh", argv[1:]...).Output()
			parse(d.Name()+"/", out)
		}
	}
	if len(pkgs) == 0 {
		return nil
	}
	return pkgs
}

func localPackages() map[string]string {
	out, err := exec.Command("dpkg-query", "-W", "-f", "${Package} ${Version}\n",
		"katzenpost", "kno", "namenlos-config-*").Output()
	if err != nil && len(out) == 0 {
		return nil
	}
	pkgs := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.Fields(line); len(f) == 2 {
			pkgs[f[0]] = f[1]
		}
	}
	if len(pkgs) == 0 {
		return nil
	}
	return pkgs
}

func localUnits(units []string) map[string]string {
	sd := verify.Platform()
	if !sd.Available() {
		return nil
	}
	out := map[string]string{}
	for _, u := range units {
		if st := sd.State(u).Active; isUnitState(st) {
			out[u] = st
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func localLabel(e track.Env) string {
	if label, _, ok := config.LocalHost(e.Config); ok {
		return label
	}

	nodes := map[string]bool{}
	for _, svc := range e.Services {
		nodes[push.NodeOf(svc)] = true
	}
	if len(nodes) != 1 {
		return ""
	}
	for n := range nodes {
		return n
	}
	return ""
}

func recordLocal(l track.Logger, e track.Env, r *ToolRunner, before *record.Sample, push bool) {
	label := localLabel(e)
	if label == "" || e.Repo == "" {
		return
	}
	h := remoteHost{Label: label, Node: "this host", Services: append([]string{}, e.Services...)}
	sort.Strings(h.Services)

	after := localSample(e, "", r.Consensus())
	if after != nil {
		after.Verified = verdictOf(r.verified)
	}
	keepBadRecord(l, e, e, label)
	target := e.TargetVersion()
	if r.Source != nil {
		target = r.Source.Tag
	}
	rec := recordFor(e, e, h, target, before, after, label)
	wrote, err := record.Write(e.Repo, e.Network, rec)
	if err != nil {
		if os.IsPermission(err) {
			l.Warnf("no deployment record written: %s is not writable by this account", record.Dir(e.Repo, e.Network))
			return
		}
		l.Warnf("could not write the deployment record: %v", err)
		return
	}
	if wrote {
		l.Logf("%s: %s", label, rec.Summary())
	}
	if !concluded(e.StateDir) {
		if wrote && push {
			l.Logf("%s: in the verify window. kno commits and pushes its deployment"+
				" record when the window closes", label)
		}
		return
	}
	landOnHost(l, e, label, rec, push)
}
