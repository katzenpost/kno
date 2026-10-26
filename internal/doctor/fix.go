package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/katzenpost/kno/internal/hostsetup"
	"github.com/katzenpost/kno/internal/service"
	"github.com/katzenpost/kno/internal/status"
	"github.com/katzenpost/kno/internal/track"
	"github.com/katzenpost/kno/internal/verify"
)

type fixable struct {
	What     string
	Commands [][]string

	Units []string

	Undo [][]string
}

func hostFixes(f hostFacts) []fixable {
	var out []fixable
	out = append(out, missingToolFixes(f.Tools)...)
	for _, fault := range f.Faults {
		out = append(out, fixable{
			What:     fmt.Sprintf("%s: create the %q account and hand it its state", fault.Unit, fault.User),
			Commands: userFaultArgv(fault),
		})
	}
	var shadows []string
	units := map[string][]string{}
	shadow := func(path, unit string) {
		if path == "" {
			return
		}
		if _, ok := units[path]; !ok {
			shadows = append(shadows, path)
		}
		units[path] = append(units[path], unit)
	}
	for _, u := range f.Units {
		shadow(u.Shadow, u.Unit)
	}
	shadow(f.ClientShadow, service.Client)
	for _, p := range shadows {
		out = append(out, fixable{
			What:     fmt.Sprintf("%s: systemd runs %s, not the packaged unit", strings.Join(units[p], ", "), p),
			Commands: supersedeArgv(p, units[p]...),
			Units:    units[p],
			Undo:     unsupersedeArgv(p, units[p]...),
		})
	}

	if f.ClientInstalled && !f.ClientConfig && f.Client.Active != "active" && !f.ClientWanted {
		out = append(out, fixable{
			What: "kpclientd: no " + orString(f.ClientConfigPath, service.KnoClientConfig) +
				", so it cannot start. Disable it. After that, kno verify cannot ask for a consensus on this host.",
			Commands: [][]string{{"systemctl", "disable", "--now", "kpclientd"}},
		})
	}

	if argv := clientStart(f); argv != nil {
		what := "kpclientd: enabled and has its config, but is not running"
		switch {
		case f.ClientJail != "":
			what = "kpclientd in the jail " + f.ClientJail + ": has its config, but is not running"
		case !f.Client.Enabled:
			what = "kpclientd: has its config and is meant to run here, but is disabled"
		}
		out = append(out, fixable{What: what, Commands: [][]string{argv}})
	}
	return out
}

func clientStart(f hostFacts) []string {
	if !f.ClientInstalled || !f.ClientConfig || !f.ClientWanted || f.ClientMasked ||
		f.Client.Active == "active" || f.Client.Restarts > verify.DefaultMaxRestarts {
		return nil
	}
	switch {
	case f.ClientJail != "":
		return []string{"service", "jail", "start", f.ClientJail}
	case !f.Client.Enabled:
		return []string{"systemctl", "enable", "--now", service.Client}
	}
	return []string{"systemctl", "start", service.Client}
}

func userFaultArgv(f status.UserFault) [][]string {
	return [][]string{
		{"groupadd", "--system", f.User},
		{"useradd", "--system", "--gid", f.User, "--home-dir", f.StateDir,
			"--no-create-home", "--shell", "/usr/sbin/nologin", f.User},
		{"install", "-d", "-o", f.User, "-g", f.User, "-m", "0700", f.StateDir},
		{"chown", "-R", f.User + ":" + f.User, f.StateDir},
		{"chown", "root:" + f.User, f.Config},
		{"chmod", "0640", f.Config},
		{"systemctl", "restart", f.Unit},
	}
}

func supersedeArgv(path string, units ...string) [][]string {
	return [][]string{
		{"mv", "--no-clobber", path, path + supersededSuffix},
		{"systemctl", "daemon-reload"},
		append([]string{"systemctl", "restart"}, units...),
	}
}

func unsupersedeArgv(path string, units ...string) [][]string {
	return [][]string{
		{"mv", "--no-clobber", path + supersededSuffix, path},
		{"systemctl", "daemon-reload"},
		append([]string{"systemctl", "restart"}, units...),
	}
}

const supersededSuffix = ".superseded-by-kno"

func destructive(argv []string) bool {
	switch filepath.Base(argv[0]) {
	case "rm", "rmdir", "userdel", "groupdel", "truncate", "shred", "dd", "mkfs", "kno":
		return true
	case "mv":

		if len(argv) != 4 || argv[1] != "--no-clobber" {
			return true
		}
		from, to := argv[2], argv[3]
		return to != from+supersededSuffix && from != to+supersededSuffix
	}
	for _, a := range argv {
		if strings.ContainsAny(a, ">|&;") {
			return true
		}
	}
	return false
}

var runArgv = func(ctx context.Context, w io.Writer, argv []string) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = w, w
	return cmd.Run()
}

var euid = os.Geteuid

func alreadyThere(argv []string, err error) bool {
	var ee *exec.ExitError
	return (argv[0] == "groupadd" || argv[0] == "useradd") &&
		errors.As(err, &ee) && ee.ExitCode() == 9
}

func staleNote(w io.Writer, f hostFacts) {
	if len(f.Stale) == 0 {
		return
	}
	fmt.Fprintf(w, "--fix does not remove the %d old kno files. To remove them, run this on %s:\n",
		len(f.Stale), f.Label)
	fmt.Fprintf(w, "    sudo KNO_USER=%s kno bootstrap --package\n", orString(f.UpdaterUser, "kno"))
}

func ApplyFixes(ctx context.Context, w io.Writer, e track.Env, f hostFacts, dry bool) error {
	fixes := hostFixes(f)
	if len(fixes) == 0 {

		fmt.Fprintln(w, "nothing here that --fix knows how to repair")
		staleNote(w, f)
		var down []string
		for _, u := range f.Units {
			if u.Installed && (u.State.Failed || u.State.Active != "active") {
				down = append(down, u.Unit)
			}
		}
		if len(down) == 0 {
			if len(f.Stale) == 0 {
				fmt.Fprintln(w, "and nothing is down: this host looks healthy")
			}
			return nil
		}
		fmt.Fprintf(w, "\nbut %s is not running. Run kno doctor on %s. It shows the exit\n", strings.Join(down, ", "), f.Label)
		fmt.Fprintln(w, "status and the last lines of the service log, which give the cause.")
		fmt.Fprintln(w, "If the packages on this host are older than the newest build, install them.")
		fmt.Fprintf(w, "On the organizer, run: kno update --host %s\n", f.Label)
		fmt.Fprintln(w, "On a host that updates itself, run: kno update")
		return nil
	}
	if !dry && euid() != 0 {
		return fmt.Errorf("--fix needs root: run 'sudo kno doctor --fix' (or --fix --dry-run to see the commands)")
	}
	for _, fx := range fixes {
		for _, argv := range fx.Commands {
			if destructive(argv) {
				return fmt.Errorf("--fix does not run %q. --fix runs no command that can remove a file, and it does not run kno", strings.Join(argv, " "))
			}
		}
	}
	for _, fx := range fixes {
		fmt.Fprintf(w, "==> %s\n", fx.What)
		for i, argv := range fx.Commands {
			fmt.Fprintf(w, "    %s\n", strings.Join(argv, " "))
			if dry {
				continue
			}
			err := runArgv(ctx, w, argv)
			if err == nil {
				continue
			}
			if alreadyThere(argv, err) {
				fmt.Fprintf(w, "    (already present, continuing)\n")
				continue
			}
			if i > 0 && len(fx.Undo) > 0 {
				fmt.Fprintln(w, "    the repair did not complete; undoing it")
				undo(ctx, w, fx)
			}
			return fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
		}
		if !dry {
			confirmOrUndo(ctx, w, fx, f.Label)
		}
	}
	staleNote(w, f)
	if dry {
		fmt.Fprintln(w, "\n--dry-run: nothing was changed")
		return nil
	}
	fmt.Fprintln(w, "\n--fix is complete. Run kno doctor again to see the result.")
	return nil
}

func missingToolFixes(all []Status) []fixable {
	mgr, err := hostsetup.DetectManager()
	if err != nil {
		return nil
	}
	var pkgs, names []string
	seen := map[string]bool{}
	for _, s := range all {

		pkg := s.Tool.Install
		if s.Found || !s.Checked || pkg == "" {
			continue
		}
		if seen[pkg] {
			continue
		}
		seen[pkg] = true
		pkgs = append(pkgs, pkg)
		names = append(names, s.Name)
	}
	if len(pkgs) == 0 {
		return nil
	}
	sort.Strings(pkgs)
	sort.Strings(names)
	return []fixable{{
		What:     fmt.Sprintf("install %s, which %s needs", strings.Join(pkgs, " "), strings.Join(names, " ")),
		Commands: installArgv(mgr, pkgs),
	}}
}

func installArgv(manager string, pkgs []string) [][]string {
	switch manager {
	case hostsetup.ManagerApt:
		return [][]string{
			{"env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "update", "-qq"},
			append([]string{"env", "DEBIAN_FRONTEND=noninteractive",
				"apt-get", "install", "-y", "--no-install-recommends"}, pkgs...),
		}
	case hostsetup.ManagerPkg:
		return [][]string{append([]string{"pkg", "install", "-y"}, pkgs...)}
	}
	return nil
}

const settleDelay = 3 * time.Second

func confirmOrUndo(ctx context.Context, w io.Writer, fx fixable, label string) {
	if len(fx.Units) == 0 || len(fx.Undo) == 0 {
		return
	}
	time.Sleep(settleDelay)
	down := inactive(ctx, fx.Units)
	if len(down) == 0 {
		fmt.Fprintf(w, "    %s is active\n", strings.Join(fx.Units, ", "))
		return
	}
	fmt.Fprintf(w, "    %s did NOT come back; undoing this repair\n", strings.Join(down, ", "))
	if !undo(ctx, w, fx) {
		return
	}
	time.Sleep(settleDelay)
	if down = inactive(ctx, fx.Units); len(down) == 0 {
		fmt.Fprintf(w, "    %s is active again on the file that was there before.\n", strings.Join(fx.Units, ", "))
		fmt.Fprintln(w, "    That file is doing something the packaged unit does not."+
			" Diff them before trying this again.")
		return
	}
	for _, u := range down {
		fmt.Fprintf(w, "    %s is not active after the undo. Examine the unit on %s: journalctl -u %s -n 50\n",
			u, label, u)
	}
}

func undo(ctx context.Context, w io.Writer, fx fixable) bool {
	for _, argv := range fx.Undo {
		fmt.Fprintf(w, "    %s\n", strings.Join(argv, " "))
		if err := runArgv(ctx, w, argv); err != nil {
			fmt.Fprintf(w, "    the undo itself failed: %v\n", err)
			fmt.Fprintf(w, "    PUT IT BACK BY HAND: mv %s %s && systemctl daemon-reload"+
				" && systemctl restart %s\n",
				fx.Undo[0][2], fx.Undo[0][3], strings.Join(fx.Units, " "))
			return false
		}
	}
	return true
}

func inactive(ctx context.Context, units []string) []string {
	var out []string
	for _, u := range units {
		b, _ := exec.CommandContext(ctx, "systemctl", "is-active", u).Output()
		if strings.TrimSpace(string(b)) != "active" {
			out = append(out, u)
		}
	}
	return out
}
