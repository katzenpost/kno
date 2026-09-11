package doctor

import (
	"cmp"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/hostsetup"
	"github.com/katzenpost/kno/internal/pkg/freebsd"
	"github.com/katzenpost/kno/internal/service"
	"github.com/katzenpost/kno/internal/status"
	"github.com/katzenpost/kno/internal/track"
	"github.com/katzenpost/kno/internal/verify"
)

var packagedDirs = []string{"/usr/lib/systemd/system", "/lib/systemd/system"}

var unitDirs = append(append([]string{}, packagedDirs...), "/etc/systemd/system")

func rcInstalled(s service.Service, unit string) bool {
	_, err := os.Stat(s.HostPath("/usr/local/etc/rc.d/" + unit))
	return err == nil
}

func unitInstalled(unit string) bool { return unitFileIn(unitDirs, unit) }

func packagedUnit(unit string) bool { return unitFileIn(packagedDirs, unit) }

func unitFileIn(dirs []string, unit string) bool {
	files := []string{unit + ".service"}
	if at := strings.Index(unit, "@"); at > 0 {
		files = append(files, unit[:at+1]+".service")
	}
	for _, d := range dirs {
		for _, f := range files {
			if _, err := os.Stat(filepath.Join(d, f)); err == nil {
				return true
			}
		}
	}
	return false
}

type unitReport struct {
	Service string
	Unit    string

	User string

	StateDir  string
	Config    string
	Keys      KeyReport
	Installed bool

	Masked bool
	Shadow string
	State  verify.UnitState

	Prov provenance
}

type hostFacts struct {
	Systemd bool
	Units   []unitReport
	Faults  []status.UserFault
	Stale   []string

	UpdaterUser string

	Thinclient, ThinclientWhy string

	Client          verify.UnitState
	ClientInstalled bool
	ClientWanted    bool

	ClientUser string

	ClientConfig bool

	ClientShadow string
	ClientMasked bool
	ClientJail   string

	ClientConfigPath string

	Tools []Status

	Label string
}

func gatherHost(e track.Env, sc verify.Systemd) hostFacts {
	f := hostFacts{
		Systemd:     sc.Available(),
		UpdaterUser: cmp.Or(os.Getenv("KNO_USER"), updaterFor(runtime.GOOS)),
		Tools:       CheckAll(),
		Label:       hostLabel(e),
	}
	f.ClientWanted = e.Config.RunsKpclientd(f.Label)
	svcs := append([]string{}, e.Services...)
	sort.Strings(svcs)
	jailed := false
	for _, svc := range svcs {
		u := unitReport{Service: svc, Unit: svc}
		s, err := service.ResolveService(e.Repo, e.Network, svc)
		if err == nil && s.Unit != "" {
			u.Unit = s.Unit
		}
		jailed = jailed || s.Jail != ""

		if s.Jail != "" {
			u.Installed = rcInstalled(s, u.Unit)
			u.Unit = verify.JailUnit(s.Jail, u.Unit)
		} else {
			u.Installed = unitInstalled(u.Unit)
		}
		if u.Installed && s.Jail == "" {
			u.Masked = masked(u.Unit)
			u.Shadow = shadowed(u.Unit)
		}
		u.Prov = gatherProvenance(svc)
		if err == nil {
			u.StateDir, u.Config = s.HostPath(s.StateDir), s.HostPath(s.ConfigDst)
			if s.Jail == "" {
				u.User = s.ServiceUser
			}
		}
		if f.Systemd && u.Installed {
			u.State = sc.State(u.Unit)

			if c := unitConfigPath(u.Unit); c != "" && s.Jail == "" {
				u.Config = c
			}
		}
		if u.StateDir != "" || u.Config != "" {
			u.Keys = ScanKeysIn(u.StateDir, u.Config, s.HostPath)
		}
		f.Units = append(f.Units, u)
	}
	f.Faults = status.UserFaults(e)
	f.Stale = hostsetup.StaleOnHost(f.UpdaterUser)
	if len(f.Units) > 0 && jailed {
		c := service.Service{Jail: config.LocalClientJail(e.Config)}
		f.Thinclient = c.HostPath(verify.Thinclient(true))
		f.ThinclientWhy = verify.ThinclientProblem(f.Thinclient)
		if rcInstalled(c, service.Client) {
			f.Client = sc.State(verify.JailUnit(c.Jail, service.Client))
			f.ClientInstalled, f.ClientConfig = true, true
			f.ClientJail = c.Jail
		}
		return f
	}
	if len(f.Units) > 0 {
		f.Thinclient = verify.Thinclient(false)
		f.ThinclientWhy = verify.ThinclientProblem(f.Thinclient)
		if unitInstalled("kpclientd") {
			f.Client = sc.State("kpclientd")
			f.ClientInstalled = true
			f.ClientUser = unitUser("kpclientd")
			f.ClientConfigPath = clientConfigPath()
			_, err := os.Stat(f.ClientConfigPath)
			f.ClientConfig = err == nil
			f.ClientMasked = masked("kpclientd")
			f.ClientShadow = shadowed("kpclientd")
		}
	}
	return f
}

func hostLabel(e track.Env) string {
	if l := os.Getenv("KNO_DOCTOR_LABEL"); l != "" {
		return l
	}
	if l, _, ok := config.LocalHost(e.Config); ok {
		return l
	}
	return "<label>"
}

func reportUpdate(r *Result, force bool) {
	opt := ""
	if force {
		opt = " --force"
	}
	r.line("       On the organizer, run:")
	r.line("           kno update --host %s%s", r.label, opt)
	r.line("       On a host that updates itself, run:")
	r.line("           sudo kno update%s", opt)
	if force {
		r.line("       This installs the packages again and starts a new verify window.")
	}
}

func clientConfigPath() string {
	if p := unitConfigPath("kpclientd"); p != "" {
		return p
	}
	return service.KnoClientConfig
}

func unitConfigPath(unit string) string {
	for _, prop := range []string{"ExecStart", "ExecStartPre"} {
		argv := parseExecArgv(showUnit(unit, prop))
		for i, a := range argv {
			if (a == "-f" || a == "-c" || a == "--config") && i+1 < len(argv) {
				return argv[i+1]
			}
			if v, ok := strings.CutPrefix(a, "--config="); ok {
				return v
			}
		}
	}
	return ""
}

func reportHost(r *Result, f hostFacts) {
	r.section(hostSectionTitle())
	switch {
	case len(f.Units) == 0:
		r.check("services", true, "none placed on this host (nothing to check here)")
	case !f.Systemd:
		r.warn("services", "no systemd here; unit state is unknown, not bad")
		for _, u := range f.Units {
			r.line("       %-24s unit %s", u.Service, u.Unit)
		}
	default:
		reportUnits(r, f.Units)
	}
	reportUptime(r, gatherUptime(f.Units), f.Units)
	reportFaults(r, f.Faults)
	reportConsensusPath(r, f)
	reportStale(r, f.Stale, f.UpdaterUser)

	reportAccounts(r, gatherAccounts(f), selfAccount())
}

func reportConsensusPath(r *Result, f hostFacts) {
	if f.Thinclient == "" && f.ThinclientWhy == "" {
		return
	}
	if f.ThinclientWhy != "" {

		if !f.ClientInstalled {
			r.warn("consensus check", "%s", f.ThinclientWhy)
			r.line("       nothing on this host serves it either, so this may simply not be")
			r.line("       how this host checks consensus. 'kno verify' will say the same.")
			return
		}
		r.check("consensus check", false, "%s", f.ThinclientWhy)
		r.line("       kpclientd is installed here, so this host must ask for a consensus,")
		r.line("       and without this file it cannot. kno verify reports this as")
		r.line("       \"cannot check consensus\", not as a network fault.")
		r.line("       To install the file again:")
		reportUpdate(r, true)
		r.line("       Or write the file by hand on %s. It holds no secret, only the", r.label)
		body, sudo := thinclientBody, "sudo "
		if f.ClientJail != "" {
			body, sudo = string(freebsd.ClientSocket([]byte(thinclientBody))), ""
			r.line("       address of the local kpclientd:")
		} else {
			r.line("       address of the local kpclientd, and dpkg accepts a matching file:")
		}
		r.line("           %sinstall -d %s", sudo, filepath.Dir(f.Thinclient))
		r.line("           printf '%%s\\n' \\")
		for _, l := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
			r.line("             '%s' \\", l)
		}
		r.line("             | %stee %s", sudo, f.Thinclient)
		return
	}
	if f.ClientShadow != "" {
		reportShadowed(r, "kpclientd", f.ClientShadow)
	}
	r.check("consensus check", true, "%s is readable", f.Thinclient)
	if !f.ClientInstalled || f.Client.Active == "active" {
		return
	}
	cfg := orString(f.ClientConfigPath, service.KnoClientConfig)
	if !f.ClientConfig {
		if !f.Client.Enabled {
			r.check("kpclientd", true, "is disabled and not running, which is right"+
				" for a host with no %s", cfg)
			return
		}
		r.warn("kpclientd", "is %s; it serves the socket %s names, so every"+
			" consensus check on this host will fail while it is down",
			orUnknown(f.Client.Active), f.Thinclient)
		r.line("       kpclientd cannot start without %s.", cfg)
		r.line("       If this host must run kpclientd, install the config again:")
		reportUpdate(r, true)
		r.line("       If this host must not run kpclientd, run this on %s:", r.label)
		r.line("           sudo systemctl disable --now kpclientd")
		r.line("       After that, kno verify cannot ask for a consensus on %s.", r.label)
		return
	}
	state := "is " + orUnknown(f.Client.Active)
	switch {
	case f.ClientMasked:
		state = "is masked"
	case !f.Client.Enabled:
		state = "is disabled and not running"
	}
	if f.ClientWanted {
		r.check("kpclientd", false, "has %s, but %s. Each consensus check on this"+
			" host dials the socket that it serves, so kno verify cannot ask for a"+
			" consensus.", cfg, state)
	} else {
		r.warn("kpclientd", "has %s, but %s. kno.toml sets kpclientd = false for"+
			" this host, so kno verify cannot ask for a consensus here.", cfg, state)
	}
	if argv := clientStart(f); argv != nil {
		cmd := strings.Join(argv, " ")
		if f.ClientJail == "" {
			cmd = "sudo " + cmd
		}
		r.line("       To start it, run this on %s:", r.label)
		r.line("           %s", cmd)
		r.line("       Or run kno doctor --fix, which runs that command.")
		return
	}
	if f.ClientMasked {
		r.line("       --fix does not change a mask. If nobody masked it on purpose,")
		r.line("       run this on %s:", r.label)
		r.line("           sudo systemctl unmask kpclientd")
		return
	}
	status := "sudo systemctl status kpclientd"
	if f.ClientJail != "" {
		status = verify.ClientHints(f.ClientJail)[1]
	}
	r.line("       To see why, run this on %s:", r.label)
	r.line("           %s", status)
}

const thinclientBody = "[Dial]\n  [Dial.Unix]\n    Address = \"@katzenpost\"\n"

func reportUnits(r *Result, units []unitReport) {
	for _, u := range units {

		unmanaged := reportProvenance(r, u.Prov)
		if !u.Installed {
			r.warn(u.Service, "%s is not installed on this host. On the organizer, run:"+
				" kno update --host %s. On a host that updates itself, run: sudo kno update.", u.Unit, r.label)
			continue
		}
		if u.Shadow != "" {
			reportShadowed(r, u.Unit, u.Shadow)
		}
		var w unitWhy
		switch {
		case u.Masked:
			r.check(u.Service, false, "%s is masked, so systemd does not start it", u.Unit)
			r.line("       --fix does not change a mask. If nobody masked it on purpose,")
			r.line("       run this on %s:", r.label)
			r.line("           sudo systemctl unmask %s", u.Unit)
		case u.State.Failed:
			r.check(u.Service, false, "%s is failed", u.Unit)
			w = withValidation(whyNotRunning(u.Unit), u.Unit, u.User)
			reportWhy(r, u.Unit, w)
		case u.State.Active != "active":

			r.check(u.Service, false, "%s is %s, not active", u.Unit, orUnknown(u.State.Active))
			w = withValidation(whyNotRunning(u.Unit), u.Unit, u.User)
			reportWhy(r, u.Unit, w)
		case u.State.Restarts > verify.DefaultMaxRestarts:

			r.check(u.Service, false, "%s restarted %d times. The limit is %d,"+
				" the default of kno verify --max-restarts.",
				u.Unit, u.State.Restarts, verify.DefaultMaxRestarts)
			r.line("       The unit is active now. Run kno doctor two times, 1 min apart.")
			r.line("       If the count increases, the service is crashing. To see the")
			r.line("       cause in the log, run this on %s:", r.label)
			r.line("           journalctl -u %s -n 50", u.Unit)
			w = withValidation(whyNotRunning(u.Unit), u.Unit, u.User)
			reportWhy(r, u.Unit, w)
		case u.State.Restarts > 0:
			r.check(u.Service, true, "%s is active (%d restarts)", u.Unit, u.State.Restarts)
		default:
			r.check(u.Service, true, "%s is active", u.Unit)
		}

		if !unmanaged {
			reportKeys(r, u.Service, u.Keys)
			reportKeyGap(r, u.Service, u.Keys, w)
		} else if len(u.Keys.Mismatches) > 0 {
			r.line("       (%d key file(s) also disagree with their own names;",
				len(u.Keys.Mismatches))
			r.line("        the config package ships the correct public keys)")
		}
	}
}

func reportKeyGap(r *Result, svc string, k KeyReport, w unitWhy) {
	if len(k.Mismatches) > 0 || !mentionsKeyFault(w) {
		return
	}
	r.check(svc, false, "the daemon is refusing a key file, but this scan found nothing wrong")
	r.line("       scanned: %s", k.Coverage())
	if len(k.Blocked) > 0 {
		r.line("       and it could not read %d of them (listed above) -- run 'sudo kno doctor'.", len(k.Blocked))
	} else {
		r.line("       the file is somewhere this scan does not reach. The key the server")
		r.line("       opens as a public key is identity.public.pem in its DataDir.")
	}
	r.line("       DO NOT delete or regenerate anything while working this out:")
	r.line("       run 'kno backup-keys' first -- it copies, it never moves.")
}

func mentionsKeyFault(w unitWhy) bool {
	const marker = "wrong key type"
	if strings.Contains(strings.ToLower(w.Validation), marker) {
		return true
	}
	for _, l := range w.Log {
		if strings.Contains(strings.ToLower(l), marker) {
			return true
		}
	}
	return false
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func reportFaults(r *Result, faults []status.UserFault) {
	for _, f := range faults {
		r.check(f.Service, false, "%s runs as %q, which does not exist on this host;"+
			" systemd fails it with status=217/USER and says nothing about why", f.Unit, f.User)
		r.line("       its config package predates the fix that creates the account;")
		r.line("       installing a current one repairs it. To fix it now:")
		for _, c := range f.Repair() {
			r.line("           %s", c)
		}
		r.line("       your keys in %s are untouched by this: chown never deletes.", f.StateDir)
	}
}

func reportStale(r *Result, stale []string, user string) {
	if len(stale) == 0 {
		return
	}
	r.check("old kno files", false, "files from an older kno that this kno does not use (%d): %s",
		len(stale), strings.Join(stale, " "))
	r.line("       To remove them, run this on %s:", r.label)
	r.line("           sudo KNO_USER=%s kno bootstrap --package", user)
}

func orString(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func hostSectionTitle() string {
	if l := os.Getenv("KNO_DOCTOR_LABEL"); l != "" {
		return l + " (reached over ssh)"
	}
	return "this host"
}

func updaterFor(goos string) string {
	if goos == "freebsd" {
		return freebsd.CronUser
	}
	return "kno"
}
