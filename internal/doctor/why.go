package doctor

import (
	"os"
	"os/exec"
	"os/user"
	"slices"
	"strconv"
	"strings"

	"github.com/katzenpost/kno/internal/cli"
)

var systemdExit = map[int]string{
	200: "could not change to the working directory (WorkingDirectory=)",
	201: "could not set up standard input/output",
	202: "could not set up the file descriptor store",
	203: "could not execute the binary: missing, not executable, or the wrong architecture",
	204: "could not set up the memory deny/write-execute restriction",
	205: "too many processes",
	206: "could not apply a resource limit",
	207: "could not set up the socket or file descriptors",
	208: "could not set up the terminal",
	209: "could not apply the seccomp filter",
	210: "could not set up the namespace",
	212: "could not set up the network namespace",
	213: "could not set the capability bounding set",
	214: "could not set up the user namespace",
	215: "could not apply a personality setting",
	216: "could not change the root directory",
	217: "the User= account does not exist on this host",
	218: "could not set the supplementary groups",
	219: "could not set up the control group",
	222: "could not set the OOM score",
	224: "could not apply the apparmor profile",
	225: "could not set up the private mount namespace",
	226: "could not set up the namespace (NAMESPACE)",
	227: "could not drop capabilities",
	228: "could not set up the seccomp filter (SECCOMP)",
	229: "could not set up the SELinux context",
	232: "could not set up the state directory",
	233: "could not set up the runtime directory",
	235: "could not set up the chown of a runtime path",
	236: "could not set up the SMACK label",
	238: "could not set up the credentials",
	242: "could not set up the group of a runtime path",
}

type unitWhy struct {
	Failing string

	Status  int
	Errno   int
	Meaning string

	Log []string

	Validation string
}

var showUnit = func(unit, prop string) string {
	out, err := exec.Command("systemctl", "show", unit, "-p", prop, "--value").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func whyNotRunning(unit string) unitWhy {
	w := unitWhy{}

	if s := showUnit(unit, "ExecMainStatus"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n != 0 {
			w.Status = n
			w.Meaning = systemdExit[n]
		}
	}
	if w.Status == 0 {
		if n, err := strconv.Atoi(showUnit(unit, "StatusErrno")); err == nil && n != 0 {
			w.Errno = n
		}
	}
	w.Failing = showUnit(unit, "ExecMainStartTimestamp")
	if t := showUnit(unit, "Result"); t != "" && t != "success" {
		w.Meaning = strings.TrimSpace(w.Meaning + " (systemd result: " + t + ")")
	}
	w.Log = lastJournal(unit, 8)
	return w
}

func withValidation(w unitWhy, unit, asUser string) unitWhy {
	if msg, failed := validationError(unit, asUser); failed && msg != "" {
		w.Validation = msg
	}
	return w
}

func lastJournal(unit string, n int) []string {
	out, err := exec.Command("journalctl", "-u", unit, "-n", strconv.Itoa(n),
		"--no-pager", "--output=cat").Output()
	if err != nil {
		return nil
	}
	var lines []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimRight(l, "\r"); strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

func reportWhy(r *Result, unit string, w unitWhy) {
	if w.Status != 0 {
		meaning := w.Meaning
		if meaning == "" {
			meaning = "the service itself exited with this status; the log below is its own"
		}
		r.line("       last exit: status=%d -- %s", w.Status, meaning)
	}
	if w.Errno != 0 {
		r.line("       last error: errno=%d, which the service gave to systemd", w.Errno)
	}
	if w.Validation != "" {
		r.line("       The daemon refuses the config. It gives:")
		for _, l := range strings.Split(w.Validation, "\n") {
			r.line("         %s", strings.TrimRight(l, "\r"))
		}
		r.line("       The usual cause is a config package older than the binary.")
		r.line("       To install the newest packages:")
		reportUpdate(r, false)
	}
	if len(w.Log) == 0 {
		r.line("       journalctl -u %s -n 50 --no-pager", unit)
		return
	}

	if v := classifyJournal(w.Log); v.Kind != "" {
		r.line("       %s: %s", v.Kind, v.Line)
		if v.Hint != "" {
			for _, ln := range strings.Split(cli.Wrap(v.Hint, cli.Width-7, "       "), "\n") {
				r.line("%s", ln)
			}
		}
		r.line("       full log: journalctl -u %s -n 50 --no-pager", unit)
		return
	}
	r.line("       last from the service:")
	for _, l := range interesting(w.Log, 6) {
		r.line("         %s", l)
	}
	r.line("       full log: journalctl -u %s -n 50 --no-pager", unit)
}

func execStartPre(unit string) []string {
	return parseExecArgv(showUnit(unit, "ExecStartPre"))
}

func parseExecArgv(v string) []string {
	i := strings.Index(v, "argv[]=")
	if i < 0 {
		return nil
	}
	rest := v[i+len("argv[]="):]
	if j := strings.Index(rest, " ; "); j >= 0 {
		rest = rest[:j]
	}
	return strings.Fields(rest)
}

func validationError(unit, asUser string) (string, bool) {
	argv := execStartPre(unit)
	if !slices.Contains(argv, "--validate-only") {
		return "", false
	}

	if asUser != "" && asUser != currentUser() {
		if os.Geteuid() != 0 {
			return "", false
		}
		runuser, err := exec.LookPath("runuser")
		if err != nil {
			return "", false
		}
		argv = append([]string{runuser, "-u", asUser, "--"}, argv...)
	}
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	if err == nil {
		return "", false
	}
	return errorLines(string(out)), true
}

func currentUser() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return ""
}

func errorLines(out string) string {
	var keep []string
	for _, l := range strings.Split(out, "\n") {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		low := strings.ToLower(t)

		if low == "usage" || strings.HasPrefix(low, "usage:") ||
			low == "flags" || low == "commands" || low == "examples" {
			break
		}
		keep = append(keep, t)
		if len(keep) >= 6 {
			break
		}
	}
	return strings.Join(keep, "\n")
}
