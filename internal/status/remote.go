package status

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/katzenpost/kno/internal/argcheck"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/parallel"
	"github.com/katzenpost/kno/internal/pkg/freebsd"
	"github.com/katzenpost/kno/internal/service"
	"github.com/katzenpost/kno/internal/sshx"
	"github.com/katzenpost/kno/internal/track"
	"github.com/katzenpost/kno/internal/verify"
)

const sshTimeout = 90 * time.Second

func timeout() time.Duration {
	if v := os.Getenv("KNO_STATUS_SSH_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return sshTimeout
}

const unknown = "unreachable"

type Ident struct{ Arch, OS string }

func askHosts(e track.Env) (map[string]string, map[string]string, map[string][]string, map[string]Ident) {
	if os.Getenv("KNO_STATUS_SSH") == "0" {
		return nil, nil, nil, nil
	}
	ssh := sshx.Program()
	if _, err := exec.LookPath(ssh); err != nil {
		return nil, nil, nil, nil
	}

	byHost := map[string][]string{}
	labelOf := map[string]string{}
	for _, m := range e.Managed {
		node := m.SSH
		if node == "" {
			node = m.Host
		}
		byHost[node] = append(byHost[node], m.Service)
		labelOf[node] = m.Host
	}

	out := map[string]string{}
	problems := map[string]string{}
	staged := map[string][]string{}
	idents := map[string]Ident{}
	var mu sync.Mutex
	var tasks []parallel.Task
	for node, svcs := range byHost {

		label := labelOf[node]
		sort.Strings(svcs)
		tasks = append(tasks, func(ctx context.Context) error {
			states, id, waiting, why := askHost(ctx, ssh, node, label, e, svcs)
			mu.Lock()
			for svc, st := range states {
				out[stateKey(label, svc)] = st
			}
			if len(waiting) > 0 {
				staged[node] = waiting
			}
			if why != "" {
				problems[node] = why
			}
			if id.Arch != "" || id.OS != "" {
				idents[node] = id
			}
			mu.Unlock()
			return nil
		})
	}

	_ = parallel.Run(len(tasks), tasks)
	rememberStates(out)
	return out, problems, staged, idents
}

func askHost(ctx context.Context, ssh, node, label string, e track.Env, svcs []string) (map[string]string, Ident, []string, string) {
	out := map[string]string{}
	if err := argcheck.Target("ssh target", node); err != nil {
		for _, s := range svcs {
			out[s] = unknown
		}
		return out, Ident{}, nil, err.Error()
	}
	units := make([]string, 0, len(svcs))
	for _, s := range svcs {
		unit, jail := s, ""
		switch {
		case s == service.Client:
			unit = service.ClientUnit
		default:
			if r, err := service.ResolveService(e.Repo, e.Network, s); err == nil && r.Unit != "" {
				unit, jail = unitOn(e, label, r), r.RunJail()
				if jail != "" {
					jail = freebsd.KnoJailName + "." + jail
				}
			}
		}
		if err := argcheck.Arg("unit for "+s, unit); err != nil {
			unit = ""
		}
		if jail != "" && unit != "" {
			if argcheck.Arg("jail for "+s, jail) != nil {
				unit = ""
			} else {
				unit = verify.JailUnit(jail, unit)
			}
		}
		units = append(units, unit)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout())
	defer cancel()

	argv := sshx.SSH(ssh, node, hostQuery(units))
	argv = append(argv[:1:1], append([]string{"-o", "ConnectTimeout=10"}, argv[1:]...)...)

	body, _ := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()

	head, rest, _ := strings.Cut(string(body), identMark)
	tail, stagedOut, _ := strings.Cut(rest, stagedMark)
	stagedOut, runOut, _ := strings.Cut(stagedOut, runningMark)
	id := parseIdent(tail)
	staged := parseStaged(stagedOut)
	running := parseRunning(runOut)
	for i, s := range svcs {
		if i < len(units) {
			unitAsked.Store(stateKey(label, s), units[i])
		}
		if i < len(units) && running[units[i]] != "" {
			runningOf.Store(stateKey(label, s), running[units[i]])
		}
	}
	if k := running["kno-self"]; k != "" {
		runningOf.Store(stateKey(label, "kno"), k)
	}
	lines := strings.Fields(strings.TrimSpace(head))
	missed := false
	for i, s := range svcs {
		if i < len(lines) && isState(lines[i]) {
			out[s] = lines[i]
			continue
		}
		out[s] = unknown
		missed = true
	}
	if !missed {
		return out, id, staged, ""
	}

	return out, id, staged, sshReason(head)
}

func hostQuery(units []string) string {
	return unitStatesCmd(units) + "; echo '" + identMark + "'; " + sshx.HostIdent + "; echo; echo '" +
		stagedMark + `'; ls -1 ` + stagedGlob + ` 2>/dev/null | sed 's|.*/||' | sort; echo '` +
		runningMark + "'; " + runningCmd(units)
}

const runningMark = "#kno-running"

func runningCmd(units []string) string {
	var b strings.Builder
	for _, u := range units {
		if u == "" || strings.Contains(u, "/") {
			continue
		}
		fmt.Fprintf(&b, `p=$(systemctl show -p MainPID --value %[1]s 2>/dev/null); e=; `+
			`[ -n "$p" ] && [ "$p" != 0 ] && e=$(readlink /proc/$p/exe 2>/dev/null); d=current; `+
			`case "$e" in *" (deleted)") d=replaced; e=${e%% (deleted)};; esac; k=; v=; `+
			`[ -n "$e" ] && k=$(dpkg -S "$e" 2>/dev/null | head -1 | cut -d: -f1); `+
			`[ -n "$k" ] && v=$(dpkg-query -W -f='${Version}' "$k" 2>/dev/null); `+
			`echo "%[1]s ${k:--} ${v:--} $d"; `, u)
	}
	b.WriteString(`echo "kno-self kno $(dpkg-query -W -f='${Version}' kno 2>/dev/null || echo -) current"`)
	return b.String()
}

func parseRunning(out string) map[string]string {
	got := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 4 || f[1] == "-" {
			continue
		}
		info := f[1] + " " + f[2]
		if f[3] == "replaced" {
			info += ", replaced on disk since it started: restart needed"
		}
		got[f[0]] = info
	}
	return got
}

func unitStatesCmd(units []string) string {
	jailed := false
	for _, u := range units {
		jailed = jailed || strings.Contains(u, "/")
	}
	if !jailed {
		return "systemctl is-active " + strings.Join(units, " ") + " 2>/dev/null"
	}
	cmds := make([]string, len(units))
	for i, u := range units {
		jail, name, ok := strings.Cut(u, "/")
		if !ok {
			cmds[i] = "echo unknown"
			continue
		}
		cmds[i] = "p=$(jls -j " + jail + " path 2>/dev/null); if [ -n \"$p\" ] && [ -f \"$p/var/run/" + name + ".child.pid\" ]; then " +
			"if kill -0 \"$(cat \"$p/var/run/" + name + ".child.pid\")\" 2>/dev/null; then echo active; else echo inactive; fi; " +
			"elif service -j " + jail + " " + name + " status >/dev/null 2>&1; then echo active; else echo inactive; fi"
	}
	return strings.Join(cmds, "; ")
}

func parseStaged(out string) []string {
	var got []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, ".deb") || strings.HasSuffix(line, ".pkg") {
			got = append(got, line)
		}
	}
	sort.Strings(got)
	return got
}

var runningOf, unitAsked sync.Map

const identMark = "#kno-host"

const (
	stagedMark = "#kno-staged"
	stagedGlob = ".local/share/katzenpost/kno/pkgs/*/pkgs/*.deb .local/share/katzenpost/kno/pkgs/*/pkgs/*.pkg"
)

func parseIdent(s string) Ident {
	f := strings.Fields(s)
	if len(f) < 2 {
		return Ident{}
	}
	return Ident{Arch: sshx.IdentValue(f[0]), OS: sshx.IdentValue(f[1])}
}

func sshReason(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || isState(line) {
			continue
		}
		return line
	}
	return "no answer within " + timeout().String()
}

func isState(s string) bool {
	switch s {
	case "active", "inactive", "failed", "activating", "deactivating", "reloading", "unknown":
		return true
	}
	return false
}

func rememberStates(states map[string]string) {
	byHost := map[string]map[string]string{}
	for key, st := range states {
		host, svc, ok := strings.Cut(key, "/")
		if !ok {
			continue
		}
		if byHost[host] == nil {
			byHost[host] = map[string]string{}
		}
		byHost[host][svc] = st
	}
	for host, svcs := range byHost {
		p := knopath.Observed(host)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return
		}
		names := make([]string, 0, len(svcs))
		for svc := range svcs {
			names = append(names, svc)
		}
		sort.Strings(names)
		var b strings.Builder
		for _, svc := range names {

			if svcs[svc] == unknown {
				continue
			}
			unit, _ := unitAsked.Load(host + "/" + svc)
			u, _ := unit.(string)
			if u == "" {
				continue
			}
			fmt.Fprintf(&b, "%s %s %s\n", svc, u, svcs[svc])
		}
		_ = os.WriteFile(p, []byte(b.String()), 0o644)
	}
}

func ObservedStates(label string, unitOf func(svc string) string) map[string]string {
	b, err := os.ReadFile(knopath.Observed(label))
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) == 3 && isState(f[2]) && f[1] == unitOf(f[0]) {
			out[f[0]] = f[2]
		}
	}
	return out
}
