package sshx

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const Persist = "10m"

var Bounds = []string{"-o", "ConnectTimeout=15", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=4"}

func Args() []string {
	dir := controlDir()
	if dir == "" {
		return append([]string{}, Bounds...)
	}
	return append(append([]string{}, Bounds...),
		"-o", "ControlMaster=auto",

		"-o", "ControlPath="+filepath.Join(dir, "%C"),
		"-o", "ControlPersist="+Persist)
}

func Program() string {
	if v := os.Getenv("SSH"); v != "" {
		return v
	}
	return "ssh"
}

func words(ssh string) []string {
	if w := strings.Fields(ssh); len(w) > 0 {
		return w
	}
	return []string{"ssh"}
}

func SSH(ssh, node, remote string) []string {
	argv := append(words(ssh), Args()...)
	return append(argv, node, CLocale(remote))
}

const exportC = "export LC_ALL=C; "

func CLocale(remote string) string {
	if remote == "" || strings.HasPrefix(remote, exportC) {
		return remote
	}
	return exportC + strings.ReplaceAll(remote, "sudo ", "sudo LC_ALL=C ")
}

const asRoot = `$([ "$(id -u)" = 0 ] && echo env || echo sudo) LC_ALL=C `

const plain = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789@%+=:,./_-"

func Quote(s string) string {
	if s != "" && strings.Trim(s, plain) == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func AsRoot(cmd string) string { return asRoot + cmd }

func AsRootWith(mode, cmd string) string {
	switch mode {
	case "su":
		return `if [ "$(id -u)" = 0 ]; then env LC_ALL=C ` + cmd +
			`; else su root -c ` + Quote("exec /bin/sh -c "+Quote("export LC_ALL=C; "+cmd)) + `; fi`
	case "doas":
		return `if [ "$(id -u)" = 0 ]; then env LC_ALL=C ` + cmd + `; else doas env LC_ALL=C ` + cmd + `; fi`
	}
	return AsRoot(cmd)
}

func SSHForRoot(ssh, node, mode, remote string) []string {
	if mode != "su" {
		return SSH(ssh, node, remote)
	}
	argv := append(words(ssh), Args()...)
	return append(argv, "-tt", node, CLocale(remote))
}

const HostIdent = `a=$(dpkg --print-architecture 2>/dev/null) || a=;` +
	` [ -n "$a" ] || case $(uname -m 2>/dev/null) in x86_64|amd64) a=amd64;; aarch64|arm64) a=arm64;;` +
	` ppc64le|powerpc64le) a=ppc64el;; *) a=$(uname -m 2>/dev/null);; esac;` +
	` [ -r /etc/os-release ] && . /etc/os-release;` +
	` printf "%s %s-%s" "${a:--}" "${ID:-}" "${VERSION_ID:-}"`

func IdentValue(s string) string {
	if s == "-" {
		return ""
	}
	return s
}

func RsyncShell(ssh string) string {
	a := Args()
	if len(a) == 0 {
		return ""
	}
	return strings.Join(append(words(ssh), a...), " ")
}

func RsyncArgs(ssh string) []string {
	if s := RsyncShell(ssh); s != "" {
		return []string{"-e", s}
	}
	return nil
}

var tmpRoot = "/tmp"

const (
	sunPath     = 104
	socketTail  = len("/") + 40 + len(".") + 16
	maxDirBytes = sunPath - 1 - socketTail
)

func controlName() string { return fmt.Sprintf("kno-ssh-%d", os.Getuid()) }

func controlDir() string {
	if os.Getenv("KNO_SSH_MULTIPLEX") == "0" {
		return ""
	}
	var bases []string
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		bases = append(bases, filepath.Join(d, "kno-ssh"))
	}
	bases = append(bases, filepath.Join(os.TempDir(), controlName()), filepath.Join(tmpRoot, controlName()))
	for _, base := range bases {
		if len(base) <= maxDirBytes && private(base) {
			return base
		}
	}
	return ""
}

func private(dir string) bool {
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return false
	}
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() || fi.Mode().Perm()&0o077 != 0 {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}

func CheckMaster(ssh, node string) int {
	dir := controlDir()
	if dir == "" {
		return 0
	}
	w := words(ssh)
	ask := func(op string) error {
		return exec.Command(w[0], append(append(w[1:], Args()...), "-O", op, node)...).Run()
	}
	if ask("check") == nil {
		return 0
	}

	_ = ask("exit")
	removed := 0
	for _, p := range staleSockets(dir) {
		if err := os.Remove(p); err == nil {
			removed++
		}
	}
	return removed
}

func staleSockets(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		p := filepath.Join(dir, e.Name())
		fi, err := e.Info()
		if err != nil || fi.Mode()&os.ModeSocket == 0 {
			continue
		}
		c, err := net.DialTimeout("unix", p, time.Second)
		if err != nil {
			out = append(out, p)
			continue
		}
		c.Close()
	}
	return out
}
