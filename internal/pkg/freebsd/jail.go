package freebsd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	EzjailConfDir = "/usr/local/etc/ezjail"
	JailConfDir   = "/etc/jail.conf.d"
	JailRCConf    = "/etc/rc.conf.d/jail"
	KnoJailRoot   = "/var/jail/kno"
	KnoJailLogs   = "/var/log/kno"
	KatzenpostDir = KnoJailRoot + "/.kp"
	KatzenpostTag = KatzenpostDir + "/current"
)

func KatzenpostRoot(tag string) string { return filepath.Join(KatzenpostDir, tag) }

type Jail struct {
	Name     string
	Root     string
	IPs      []string
	Unit     string
	Command  []string
	HostDirs []string
	PkgRoot  string
	RootDirs []string
	StateDir string
	RunDirs  []string
	Prestart string
}

type EzjailFacts struct {
	Root  string
	IPs   []string
	NoRun bool
}

func ReadEzjail(name string) (EzjailFacts, error) {
	path := filepath.Join(EzjailConfDir, name)
	norun := false
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		norun = true
		f, err = os.Open(path + ".norun")
	}
	if err != nil {
		return EzjailFacts{}, fmt.Errorf("no ezjail config for the jail %s in %s: %v", name, EzjailConfDir, err)
	}
	defer f.Close()
	v := "jail_" + ezjailVar(name) + "_"
	var facts EzjailFacts
	facts.NoRun = norun
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, val, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(sc.Text()), "export "), "=")
		if !ok {
			continue
		}
		val = strings.Trim(val, `"'`)
		switch k {
		case v + "rootdir":
			facts.Root = val
		case v + "ip":
			for _, ip := range strings.Split(val, ",") {
				if ip = strings.TrimSpace(ip); ip != "" {
					facts.IPs = append(facts.IPs, ip)
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return EzjailFacts{}, err
	}
	if facts.Root == "" || len(facts.IPs) == 0 {
		return EzjailFacts{}, fmt.Errorf("the ezjail config of %s names no rootdir or no ip", name)
	}
	return facts, nil
}

func ezjailVar(name string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, name)
}

func (j Jail) Path() string { return filepath.Join(KnoJailRoot, j.Name) }

func (j Jail) skel() string { return filepath.Join(KnoJailRoot, ".skel", j.Name) }

func (j Jail) ConfPath() string { return filepath.Join(JailConfDir, j.Name+".conf") }

func (j Jail) ChildPidfile() string { return "/var/run/" + j.Unit + ".child.pid" }

func (j Jail) Conf() string {
	p, logs := j.Path(), filepath.Join(KnoJailLogs, j.Name)
	type mnt struct{ src, dst, typ, opts string }
	mounts := []mnt{{j.skel(), "", "nullfs", "ro"}}
	for _, d := range []string{"/bin", "/lib", "/libexec", "/usr/sbin"} {
		mounts = append(mounts, mnt{d, d, "nullfs", "ro"})
	}
	for _, d := range j.HostDirs {
		mounts = append(mounts, mnt{j.PkgRoot + d, d, "nullfs", "ro"})
	}
	for _, d := range j.RootDirs {
		mounts = append(mounts, mnt{j.Root + d, d, "nullfs", "ro"})
	}
	if j.StateDir != "" {
		mounts = append(mounts, mnt{j.Root + j.StateDir, j.StateDir, "nullfs", "rw"})
	}
	mounts = append(mounts,
		mnt{"tmpfs", "/var/run", "tmpfs", "rw,mode=755"},
		mnt{"tmpfs", "/tmp", "tmpfs", "rw,mode=1777"},
		mnt{logs, "/var/log", "nullfs", "rw"})

	dirs := []string{logs, p}
	for _, m := range mounts[1:] {
		dirs = append(dirs, j.skel()+m.dst)
	}
	dirs = append(dirs, j.skel()+"/etc", j.skel()+"/dev")

	var v4, v6 []string
	for _, ip := range j.IPs {
		addr := ip
		if _, a, ok := strings.Cut(ip, "|"); ok {
			addr = a
		}
		if strings.Contains(addr, ":") {
			v6 = append(v6, ip)
		} else {
			v4 = append(v4, ip)
		}
	}

	start := "/usr/sbin/daemon -r -P /var/run/" + j.Unit + ".pid -p " + j.ChildPidfile() +
		" -o /var/log/" + j.Unit + ".log " + strings.Join(j.Command, " ")
	if len(j.RunDirs) > 0 {
		start = "/bin/mkdir -p " + strings.Join(j.RunDirs, " ") + " && " + start
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Written by kno. kno writes it again on each install.\n")
	fmt.Fprintf(&b, "%s {\n", j.Name)
	fmt.Fprintf(&b, "\tpath = %q;\n", p)
	fmt.Fprintf(&b, "\thost.hostname = %q;\n", j.Name)
	if len(v4) > 0 {
		fmt.Fprintf(&b, "\tip4.addr = %q;\n", strings.Join(v4, ","))
	}
	if len(v6) > 0 {
		fmt.Fprintf(&b, "\tip6.addr = %q;\n", strings.Join(v6, ","))
	}
	fmt.Fprintf(&b, "\tmount.devfs;\n\tdevfs_ruleset = 4;\n")
	fmt.Fprintf(&b, "\texec.prepare = %q;\n", "/bin/mkdir -p "+strings.Join(dirs, " "))
	for i, m := range mounts {
		op := "+="
		if i == 0 {
			op = "="
		}
		fmt.Fprintf(&b, "\tmount %s %q;\n", op, fmt.Sprintf("%s %s %s %s 0 0", m.src, p+m.dst, m.typ, m.opts))
	}
	if j.Prestart != "" {
		fmt.Fprintf(&b, "\texec.prestart = %q;\n", j.Prestart)
	}
	fmt.Fprintf(&b, "\texec.start = %q;\n", start)
	fmt.Fprintf(&b, "\tstop.timeout = 30;\n\tpersist;\n}\n")
	return b.String()
}

func JailRC(names []string) string {
	return "# Written by kno.\njail_enable=\"YES\"\njail_list=\"" + strings.Join(names, " ") + "\"\n"
}
