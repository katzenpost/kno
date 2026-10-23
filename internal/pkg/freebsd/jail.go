package freebsd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/katzenpost/kno/internal/service"
)

var (
	KnoJailName = "kno"
	JailConfDir = "/etc/jail.conf.d"
	JailRCConf  = "/etc/rc.conf.d/jail"
	JailsDir    = "/var/jail"
	JailLogs    = "/var/log/jails"
	RootsDir    = "/var/db/kno/roots"
	StatesDir   = "/var/db/kno/state"
	CutoverFile = "/var/db/kno/cutover"
)

func JailRoot(jail, tag string) string { return filepath.Join(RootsDir, jail, tag) }

func CurrentRoot(jail string) string { return filepath.Join(RootsDir, jail, "current") }

func RunningRoot(jail string) string { return filepath.Join(RootsDir, jail, "running") }

func StateRoot(jail string) string { return filepath.Join(StatesDir, jail) }

func KeysDir(svc string) string { return "/usr/local/share/namenlos/" + svc + "/keys" }

func HostPath(s service.Service, p string) string {
	if s.Jail == "" || p == "" {
		return p
	}
	if rest, ok := strings.CutPrefix(p, s.StateDir); ok && s.StateDir != "" && (rest == "" || rest[0] == '/') {
		return StateRoot(s.RunJail()) + rest
	}
	return CurrentRoot(s.RunJail()) + p
}

func EnabledJails() []string {
	f, err := os.Open(JailRCConf)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "jail_list="); ok {
			out = strings.Fields(strings.Trim(v, `"'`))
		}
	}
	return out
}

type Jail struct {
	Name     string
	Unit     string
	Command  []string
	StateDir string
	RunDirs  []string
	KeysDir  string
	Prestart string
}

func (j Jail) Path() string { return filepath.Join(JailsDir, j.Name) }

func (j Jail) skel() string { return filepath.Join(JailsDir, ".skel", j.Name) }

func (j Jail) ConfPath() string { return filepath.Join(JailConfDir, j.Name+".conf") }

func (j Jail) ChildPidfile() string { return "/var/run/" + j.Unit + ".child.pid" }

func (j Jail) Conf() string {
	p, logs := j.Path(), filepath.Join(JailLogs, j.Name)
	type mnt struct{ src, dst, typ, opts string }
	mounts := []mnt{{j.skel(), "", "nullfs", "ro"}}
	for _, d := range []string{"/bin", "/lib", "/libexec", "/usr/sbin"} {
		mounts = append(mounts, mnt{d, d, "nullfs", "ro"})
	}
	mounts = append(mounts, mnt{CurrentRoot(j.Name) + "/usr/local", "/usr/local", "nullfs", "ro"})
	if j.StateDir != "" {
		mounts = append(mounts, mnt{StateRoot(j.Name), j.StateDir, "nullfs", "rw"})
	}
	mounts = append(mounts,
		mnt{"tmpfs", "/var/run", "tmpfs", "rw,mode=755"},
		mnt{"tmpfs", "/tmp", "tmpfs", "rw,mode=1777"},
		mnt{logs, "/var/log", "nullfs", "rw"})

	dirs := []string{logs, p}
	if j.StateDir != "" {
		dirs = append(dirs, StateRoot(j.Name))
	}
	for _, m := range mounts[1:] {
		dirs = append(dirs, j.skel()+m.dst)
	}
	dirs = append(dirs, j.skel()+"/etc", j.skel()+"/dev")

	start := "/usr/sbin/daemon -r -P /var/run/" + j.Unit + ".pid -p " + j.ChildPidfile() +
		" -o /var/log/" + j.Unit + ".log " + strings.Join(j.Command, " ")
	if j.KeysDir != "" && j.StateDir != "" {
		start = "/bin/cp -f " + j.KeysDir + "/* " + j.StateDir + "/ 2>/dev/null; " + start
	}
	if j.Prestart != "" {
		start = j.Prestart + "; " + start
	}
	if len(j.RunDirs) > 0 {
		start = "/bin/mkdir -p " + strings.Join(j.RunDirs, " ") + " && " + start
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Written by kno activate.\n")
	fmt.Fprintf(&b, "%s {\n", j.Name)
	fmt.Fprintf(&b, "\tpath = %q;\n", p)
	fmt.Fprintf(&b, "\thost.hostname = %q;\n", j.Name)
	fmt.Fprintf(&b, "\tip4 = \"inherit\";\n\tip6 = \"inherit\";\n")
	fmt.Fprintf(&b, "\tmount.devfs;\n\tdevfs_ruleset = 4;\n")
	fmt.Fprintf(&b, "\texec.prepare = %q;\n", "/bin/mkdir -p "+strings.Join(dirs, " "))
	for i, m := range mounts {
		op := "+="
		if i == 0 {
			op = "="
		}
		fmt.Fprintf(&b, "\tmount %s %q;\n", op, fmt.Sprintf("%s %s %s %s 0 0", m.src, p+m.dst, m.typ, m.opts))
	}
	fmt.Fprintf(&b, "\texec.start = %q;\n", start)
	fmt.Fprintf(&b, "\tstop.timeout = 30;\n\tpersist;\n}\n")
	return b.String()
}
