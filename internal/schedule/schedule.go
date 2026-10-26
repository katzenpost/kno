package schedule

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/katzenpost/kno/internal/knopath"

	"github.com/katzenpost/kno/internal/argcheck"
	"github.com/katzenpost/kno/internal/pkg/freebsd"
	"github.com/katzenpost/kno/internal/sshx"
)

const (
	ModeTimer = "timer"
	ModeCron  = "cron"
	ModeNone  = "none"
)

const (
	unitDir   = "/etc/systemd/system"
	timerUnit = "kno-update.timer"
	svcUnit   = "kno-update.service"

	CronFile   = "/etc/cron.d/kno-update"
	CronHeader = "# kno-update periodic check"
)

type Options struct {
	Repo string

	Mode string

	Host string

	SSH, Sudo string

	User string

	FreeBSD bool

	DryRun bool
}

type Step struct {
	Argv []string

	Stdin      string
	BestEffort bool
}

type Plan struct {
	Mode  string
	Host  string
	Steps []Step
}

func (p Plan) Local() bool { return p.Host == "" }

func CronLine(user string) string {
	return "*/30 * * * * " + user + " " + knopath.InstalledBin + " update >/dev/null 2>&1"
}

func CronBody(user string) string { return CronHeader + "\n" + CronLine(user) + "\n" }

func PlanFor(o Options) (Plan, error) {
	if o.FreeBSD {
		if o.Host != "" {
			return Plan{}, freebsdSchedule(o.Host)
		}
		steps, err := freebsdSteps(o)
		return Plan{Mode: o.Mode, Steps: steps}, err
	}

	if o.Mode != ModeNone {
		if err := argcheck.User("updater user", o.User); err != nil {
			return Plan{}, err
		}
	}
	sudo := strings.Fields(o.Sudo)
	p := Plan{Mode: o.Mode, Host: o.Host}
	if !KnownMode(o.Mode) {
		return Plan{}, unknownMode(o.Mode)
	}
	if o.Host == "" && len(sudo) > 0 {
		p.Steps = []Step{{Argv: append(sudo, self(), "activate", "--schedule="+o.Mode)}}
		return p, nil
	}
	if o.Host == "" {
		steps, err := localSteps(o, sudo)
		if err != nil {
			return Plan{}, err
		}
		p.Steps = steps
		return p, nil
	}

	if err := argcheck.Target("ssh target", o.Host); err != nil {
		return Plan{}, err
	}
	steps, err := remoteSteps(o)
	if err != nil {
		return Plan{}, err
	}
	p.Steps = steps
	return p, nil
}

func unitBody(sysd, name, user string) (string, error) {
	b, err := os.ReadFile(filepath.Join(sysd, name))
	if err != nil {
		return "", fmt.Errorf("the unit template this plan installs is missing: %w", err)
	}
	return ExpandUnit(string(b), user, DefaultBinDir), nil
}

const (
	DefaultUser   = "kno"
	DefaultBinDir = knopath.BinDir
)

func ExpandUnit(unit, user, bindir string) string {
	unit = strings.ReplaceAll(unit, "@KNO_USER@", user)
	return strings.ReplaceAll(unit, "@KNO_BINDIR@", bindir)
}

func localSteps(o Options, sudo []string) ([]Step, error) {
	sysd := filepath.Join(o.Repo, "systemd")
	sudoArgv := func(a ...string) []string { return append(append([]string{}, sudo...), a...) }
	units := []string{filepath.Join(unitDir, timerUnit), filepath.Join(unitDir, svcUnit)}

	switch o.Mode {
	case ModeTimer:

		svc, err := unitBody(sysd, svcUnit, o.User)
		if err != nil {
			return nil, err
		}
		return []Step{
			{Argv: sudoArgv("tee", filepath.Join(unitDir, svcUnit)), Stdin: svc},
			{Argv: sudoArgv("chmod", "0644", filepath.Join(unitDir, svcUnit))},
			{Argv: sudoArgv("install", "-m", "0644", filepath.Join(sysd, timerUnit), filepath.Join(unitDir, timerUnit))},
			{Argv: sudoArgv("systemctl", "daemon-reload")},
			{Argv: sudoArgv("systemctl", "enable", "--now", timerUnit)},
			{Argv: sudoArgv("rm", "-f", CronFile), BestEffort: true},
		}, nil
	case ModeCron:

		return []Step{
			{Argv: sudoArgv("tee", CronFile), Stdin: CronBody(o.User)},
			{Argv: sudoArgv("systemctl", "disable", "--now", timerUnit), BestEffort: true},
			{Argv: sudoArgv("rm", "-f", filepath.Join(unitDir, timerUnit)), BestEffort: true},
			{Argv: sudoArgv("systemctl", "daemon-reload"), BestEffort: true},
		}, nil
	case ModeNone:
		return []Step{
			{Argv: sudoArgv("systemctl", "disable", "--now", timerUnit), BestEffort: true},
			{Argv: sudoArgv(append(append([]string{"rm", "-f"}, units...), CronFile)...)},
			{Argv: sudoArgv("systemctl", "daemon-reload"), BestEffort: true},
		}, nil
	}
	return nil, unknownMode(o.Mode)
}

func remoteSteps(o Options) ([]Step, error) {
	if !KnownMode(o.Mode) {
		return nil, unknownMode(o.Mode)
	}
	ssh := strings.Fields(o.SSH)
	return []Step{{Argv: append(append([]string{}, ssh...), o.Host,
		sshx.AsRoot("kno activate --schedule="+o.Mode))}}, nil
}

func self() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return "kno"
}

func freebsdSteps(o Options) ([]Step, error) {
	sudo := strings.Fields(o.Sudo)
	sudoArgv := func(a ...string) []string { return append(append([]string{}, sudo...), a...) }
	rot := filepath.Join(freebsd.NewsyslogDir, "kno.conf")
	switch o.Mode {
	case ModeCron:
		return []Step{
			{Argv: sudoArgv("mkdir", "-p", filepath.Dir(freebsd.CronFile), freebsd.NewsyslogDir)},
			{Argv: sudoArgv("tee", freebsd.CronFile), Stdin: freebsd.CronBody()},
			{Argv: sudoArgv("tee", rot), Stdin: freebsd.NewsyslogKno},
		}, nil
	case ModeNone:
		return []Step{{Argv: sudoArgv("rm", "-f", freebsd.CronFile, rot)}}, nil
	case ModeTimer:
		return nil, fmt.Errorf("FreeBSD has no systemd timer. Use kno schedule cron")
	}
	return nil, unknownMode(o.Mode)
}

func freebsdSchedule(where string) error {
	return fmt.Errorf("%s runs FreeBSD jails, and kno schedule writes a systemd timer or a"+
		" Debian cron entry. On FreeBSD the kno-host-config package installs the schedule in %s:"+
		" set scheduler = \"cron\" (or \"none\") for the host in kno.toml, then push or update"+
		" the release", where, freebsd.CronFile)
}

func localCanSchedule(goos, mode string) error {
	if goos == "freebsd" {
		if mode == ModeTimer {
			return fmt.Errorf("FreeBSD has no systemd timer. Use kno schedule cron")
		}
		return nil
	}
	switch mode {
	case ModeTimer:
		if _, err := exec.LookPath("systemctl"); err != nil {
			return fmt.Errorf("a timer needs systemd, and this host has no systemctl." +
				" kno schedule is for a Debian host, not for the organizer")
		}
	case ModeCron:
		if fi, err := os.Stat(filepath.Dir(CronFile)); err != nil || !fi.IsDir() {
			return fmt.Errorf("cron mode writes %s, and this host has no %s."+
				" kno schedule is for a Debian host, not for the organizer",
				CronFile, filepath.Dir(CronFile))
		}
	}
	return nil
}

func unknownMode(m string) error {
	return fmt.Errorf("unknown schedule '%s' (timer|cron|none)", m)
}
