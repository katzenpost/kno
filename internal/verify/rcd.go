package verify

import (
	"cmp"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/service"
	"github.com/katzenpost/kno/internal/track"
)

type RcD struct{}

func (RcD) Available() bool {
	return runtime.GOOS == "freebsd" && lookPath("service") != ""
}

func (RcD) State(unit string) UnitState {
	u := UnitState{Unit: unit, Restarts: -1, Enabled: true, Active: "inactive"}
	jail, name, ok := strings.Cut(unit, "/")
	if !ok {
		if exec.Command("service", unit, "status").Run() == nil {
			u.Active = "active"
		}
		return u
	}
	if alive, known := jailDaemon(jail, name); known {
		if alive {
			u.Active = "active"
		}
		return u
	}
	if exec.Command("service", "-j", jail, name, "status").Run() == nil {
		u.Active = "active"
	}
	return u
}

var jlsPath = func(jail string) string {
	out, err := exec.Command("jls", "-j", jail, "path").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func jailDaemon(jail, name string) (alive, known bool) {
	root := jlsPath(jail)
	if root == "" {
		return false, true
	}
	b, err := os.ReadFile(filepath.Join(root, "var", "run", name+".child.pid"))
	if os.IsNotExist(err) {
		return false, false
	}
	if err != nil {
		return false, true
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return false, true
	}
	return syscall.Kill(pid, 0) == nil, true
}

func Platform() Systemd {
	if runtime.GOOS == "freebsd" {
		return RcD{}
	}
	return Systemctl{}
}

func ServiceUnit(s service.Service) string { return JailUnit(s.RunJail(), s.Unit) }

func JailUnit(jail, unit string) string {
	if jail == "" {
		return unit
	}
	return jail + "/" + unit
}

func defaultThinclient(freebsd bool) string {
	if freebsd {
		return service.ThinclientFreeBSD
	}
	return service.Thinclient
}

func Thinclient(jailed bool) string {
	return cmp.Or(os.Getenv("VERIFY_THINCLIENT"), defaultThinclient(jailed))
}

func clientInJail(o *Options, e track.Env) {
	if runtime.GOOS != "freebsd" || os.Getenv("KNO_FETCH") != "" {
		return
	}
	jail := config.LocalClientJail(e.Config)
	root := ""
	if out, err := exec.Command("jls", "-j", jail, "path").Output(); err == nil {
		root = strings.TrimSpace(string(out))
	}
	inJail(o, jail, root)
}

const jailBin = "/usr/local/bin/"

func inJail(o *Options, jail, root string) {
	o.Wrap = []string{"jexec", jail}
	o.FileRoot = root
	o.Fetch = jailTool(root, "", "kpfetch", "pq-katzenpost-fetch")
	o.Ping = jailTool(root, os.Getenv("KNO_PING"), "kpping", "pq-katzenpost-ping")
	o.CPBench = jailTool(root, os.Getenv("KNO_CPBENCH"), "pq-katzenpost-cp-bench", "cp-bench")
}

func jailTool(root, explicit string, names ...string) string {
	if explicit != "" {
		return explicit
	}
	if root == "" {
		return jailBin + names[0]
	}
	for _, n := range names {
		if lookPath(filepath.Join(root, jailBin, n)) != "" {
			return jailBin + n
		}
	}
	return ""
}

func ClientHints(jail string) []string {
	if jail != "" {
		return []string{"service jail start " + jail, "jls -j " + jail}
	}
	return []string{"sudo systemctl enable --now kpclientd", "systemctl status kpclientd"}
}

func clientHints(o Options) []string {
	if len(o.Wrap) > 0 {
		return ClientHints(o.Wrap[len(o.Wrap)-1])
	}
	return ClientHints("")
}
