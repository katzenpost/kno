package preflight

import (
	"sort"
	"strconv"
	"strings"

	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/service"
)

type Input struct {
	Host string

	State map[string]string

	Target string

	Services []string
	Units    []string
	UnitOf   map[string]string

	Migrating     bool
	Dirs, NewDirs map[string]string
	Client        bool
	ReserveMiB    int

	Packages map[string][]string

	Local       LocalState
	ReleasesDir string
}

func Judge(in Input) Report {
	r := Report{Host: in.Host}
	s := in.State

	judgeRollback(&r, in)
	judgeAnswered(&r, s, in)
	judgeKeys(&r, s, in)
	judgeNewDirs(&r, s, in)
	judgeCourier(&r, s, in)
	judgeLegacyReplicaDB(&r, s, in)
	judgeClient(&r, s, in)
	judgeVersions(&r, in)
	judgeDisk(&r, s, in.ReserveMiB)
	return r
}

func judgeRollback(r *Report, in Input) {
	l, target := in.Local, in.Target
	if !l.Exists {
		r.add(Warn, "kno state", "nothing recorded for this host yet, so there"+
			" is no release to roll back to. That is the first upgrade.")
		return
	}
	switch {
	case l.Current == "":
		r.add(Warn, "rollback", "nothing recorded as current; a failed install"+
			" has no previous release to restore")
	case l.LKG == "":
		r.add(Warn, "rollback", "on %s with no last-known-good recorded; a"+
			" failed install would leave this host on %s, the release that"+
			" failed", l.Current, l.Current)
	case !has(l.Releases, l.LKG):
		r.add(Stop, "rollback", "last-known-good is %s and it is not among the"+
			" releases on disk (%s).\n"+
			"    A failed install could not be undone. Build or push %s first,"+
			" or --force.",
			l.LKG, strings.Join(l.Releases, " "), l.LKG)
	case !l.Usable(in.ReleasesDir, l.LKG):
		r.add(Stop, "rollback", "last-known-good %s is on disk but has no"+
			" manifest directory, so activate could not install it.\n"+
			"    A rollback would fail at the same place the install did."+
			" Rebuild %s, or --force.", l.LKG, l.LKG)
	default:
		r.add(Info, "rollback", "can fall back to %s", l.LKG)
	}
	if l.Pending != "" {
		r.add(Stop, "in flight", "a deployment of %s is already pending here;"+
			" finish or clear it before starting another", l.Pending)
	}
	if has(l.Quarantined, target) {
		r.add(Stop, "quarantined", "%s is quarantined for this host: it was"+
			" installed before and did not rejoin consensus.\n"+
			"    'kno doctor' says what broke; 'kno unquarantine %s' lets it be"+
			" re-attempted.", target, target)
	}
}

func judgeAnswered(r *Report, s map[string]string, in Input) {
	if len(s) == 0 {

		return
	}
	answered := 0
	for _, svc := range in.Services {
		if s["dir:"+svc] != "" {
			answered++
		}
	}
	if len(in.Services) > 0 && answered == 0 {
		r.add(Warn, "answered", "the host answered but said nothing about any"+
			" of its %d service(s); every check below had no input",
			len(in.Services))
		return
	}
	if answered > 0 {
		r.add(Info, "answered", "read %d of %d service state director(ies)",
			answered, len(in.Services))
	}
	if n := len(installedPackages(s)); n > 0 {
		r.add(Info, "installed", "%d package(s) read from dpkg", n)
	}
	if kb, err := strconv.ParseInt(strings.TrimSpace(s["disk"]), 10, 64); err == nil && kb > 0 {
		r.add(Info, "disk", "%d GB free for the node state", kb/1024/1024)
	}
	for _, u := range in.Units {
		if st := s["unit:"+u]; st != "" {
			r.add(Info, "unit", "%s is %s", u, st)
		}
	}
}

func judgeKeys(r *Report, s map[string]string, in Input) {
	for _, svc := range in.Services {
		if s["dir:"+svc] == "no" {
			r.add(Warn, "state dir", "%s has no state directory yet; it will be"+
				" created and the node will generate a fresh identity", svc)
			continue
		}
		pub, key := s["idpub:"+svc], s["idkey:"+svc]
		if pub == "" && key == "" {
			continue
		}
		if key == "no" {
			r.add(Stop, "identity key", "%s has a state directory but no"+
				" identity.private.pem.\n"+
				"    That key is generated on the node and exists nowhere else."+
				" Installing over this\n    would not replace it -- kno refuses"+
				" to ship one -- but the service cannot run\n    without it."+
				" Restore it from a backup ('kno backup-keys' makes them) before"+
				" upgrading.", svc)
			continue
		}
		if pub == "no" && !in.Migrating {
			r.add(Warn, "identity key", "%s has its private key but no"+
				" identity.public.pem; some daemons read the public half at"+
				" startup", svc)
			continue
		}
		if key == "yes" {
			r.add(Info, "identity key", "%s has its own, which is the one"+
				" thing here that cannot be rebuilt", svc)
		}
	}
}

func judgeLegacyReplicaDB(r *Report, s map[string]string, in Input) {
	for _, svc := range in.Services {
		if s["legacydb:"+svc] != "yes" {
			continue
		}
		unit := in.UnitOf[svc]
		if unit == "" {
			unit = unitFor(svc, in.Units)
		}
		if unit != "" && s["unit:"+unit] == "active" {
			r.add(Info, "legacy db", "%s still carries replica.db; it is running"+
				" now, so the migration marker is set and the upgrade will"+
				" remove it", svc)
			continue
		}
		r.add(Stop, "legacy db", "%s carries a legacy replica.db and is not"+
			" running, so nothing here shows whether it was migrated.\n"+
			"    The target release refuses to start on an unmigrated database."+
			" Start the\n    service on its current release first: if it comes"+
			" up, the marker is set.", svc)
	}
}

func judgeNewDirs(r *Report, s map[string]string, in Input) {
	if !in.Migrating {
		return
	}
	for _, svc := range in.Services {
		first := s["newdir:"+svc]
		if first == "" {
			continue
		}
		r.add(Stop, "new state dir", "%s already has files in %s (%s, and maybe"+
			" more).\n"+
			"    The upgrade moves the state from %s, but not a name that is"+
			" there already,\n    so a file there would hide the one this"+
			" host runs on now. Make sure %s\n    holds the keys, then remove"+
			" %s.", svc, in.NewDirs[svc], first, in.Dirs[svc], in.Dirs[svc],
			in.NewDirs[svc])
	}
}

func judgeClient(r *Report, s map[string]string, in Input) {
	st := s["client"]
	if !in.Client || st == "" || st == "not-found" {
		return
	}
	switch st {
	case "masked", "masked-runtime":
		r.add(Stop, "kpclientd", "kpclientd is %s.\n"+
			"    The katzenpost upgrade stops kpclientd, and kno cannot enable a"+
			" masked unit.\n    Without kpclientd, verify cannot ask for the"+
			" consensus, and the upgrade is never promoted.\n"+
			"    Run 'systemctl unmask kpclientd' on the host first.", st)
	case "disabled":
		r.add(Info, "kpclientd", "kpclientd is disabled; the upgrade enables and starts"+
			" it, because kno.toml wants it on this host")
	default:
		r.add(Info, "kpclientd", "kpclientd is %s", st)
	}
}

func judgeVersions(r *Report, in Input) {
	installed := installedPackages(in.State)
	if len(installed) == 0 || len(in.Packages) == 0 {
		return
	}
	names := make([]string, 0, len(installed))
	for name := range installed {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		have := installed[name]
		want := Candidate(have, in.Packages[name])
		if want == "" {
			r.add(Info, "version", "%s is at %s and the release has none;"+
				" it stays", name, have)
			continue
		}
		switch {
		case have == want || name == "katzenpost" && strings.Contains(want, "+") && strings.SplitN(want, "+", 2)[0] == have:
			r.add(Info, "version", "%s stays at %s", name, have)
		case deb.CompareVersions(have, want) < 0:

			r.add(Info, "upgrade", "%s %s -> %s", name, have, want)
		default:
			r.add(Warn, "downgrade", "%s would go from %s to %s, which dpkg"+
				" orders as a downgrade; it installs anyway", name, have, want)
		}
	}
}

func installedPackages(state map[string]string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(state["pkgs"], ";") {
		k, v, ok := strings.Cut(pair, "=")
		if ok && k != "" && v != "" {
			out[k] = v
		}
	}
	return out
}

func judgeDisk(r *Report, s map[string]string, reserveMiB int) {
	kb, err := strconv.ParseInt(strings.TrimSpace(s["disk"]), 10, 64)
	if err != nil || kb <= 0 {
		return
	}
	const floor = 512 * 1024
	switch reserve := int64(reserveMiB) * 1024; {
	case kb < floor:
		r.add(Stop, "disk", "%d MB free for the node state; an install that runs out part"+
			" way through leaves dpkg half configured", kb/1024)
	case reserve > 0 && kb < reserve:
		r.add(Stop, "disk", "%d MB free for the node state. A storage replica on this host refuses"+
			" writes below %d MB free (MinFreeStorageMiB)", kb/1024, reserveMiB)
	case reserve > 0 && kb < 2*reserve:
		r.add(Warn, "disk", "%d MB free for the node state. A storage replica on this host refuses"+
			" writes below %d MB free (MinFreeStorageMiB)", kb/1024, reserveMiB)
	}
}

func judgeCourier(r *Report, s map[string]string, in Input) {
	if !in.Migrating || s["courier"] != "old=yes new=yes" {
		return
	}
	r.add(Stop, "courier", "the courier state is in %s, and %s exists already.\n"+
		"    The upgrade moves the courier state only when %s does not exist, so the\n"+
		"    courier would start with new state. Make sure %s holds the courier\n"+
		"    state, then remove %s.", service.LegacyCourierState, service.CourierState,
		service.CourierState, service.LegacyCourierState, service.CourierState)
}

func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func unitFor(svc string, units []string) string {
	for _, u := range units {
		if u == svc || strings.HasPrefix(u, svc) || strings.HasPrefix(svc, u) {
			return u
		}
	}
	return ""
}
