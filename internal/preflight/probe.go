package preflight

import (
	"fmt"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/service"
)

type Severity int

const (
	Info Severity = iota

	Warn

	Stop
)

func (s Severity) String() string {
	switch s {
	case Stop:
		return "STOP"
	case Warn:
		return "warn"
	}
	return "ok"
}

type Finding struct {
	Severity Severity

	Check string

	Detail string
}

type Report struct {
	Host     string
	Findings []Finding
}

func (r Report) Blocked() bool {
	for _, f := range r.Findings {
		if f.Severity == Stop {
			return true
		}
	}
	return false
}

func (r *Report) add(s Severity, check, detail string, a ...any) {
	if len(a) > 0 {
		detail = fmt.Sprintf(detail, a...)
	}
	r.Findings = append(r.Findings, Finding{Severity: s, Check: check, Detail: detail})
}

func Probe(stateDirs map[string]string, units []string) string {
	var b strings.Builder
	b.WriteString("set -u\n")
	b.WriteString("p() { printf '%s=%s\\n' \"$1\" \"$2\"; }\n")

	for svc, dir := range stateDirs {
		fmt.Fprintf(&b, "D=%s\n", shq(dir))
		fmt.Fprintf(&b, "p dir:%s \"$(test -d \"$D\" && echo yes || echo no)\"\n", svc)
		fmt.Fprintf(&b, "p idpub:%s \"$(test -f \"$D\"/identity.public.pem && echo yes || echo no)\"\n", svc)
		fmt.Fprintf(&b, "p idkey:%s \"$(test -f \"$D\"/identity.private.pem && echo yes || echo no)\"\n", svc)

		fmt.Fprintf(&b, "p legacydb:%s \"$(test -e \"$D\"/replica.db && echo yes || echo no)\"\n", svc)
	}
	for _, u := range units {
		fmt.Fprintf(&b, "p unit:%s \"$(systemctl is-active %s 2>/dev/null || true)\"\n", u, shq(u))
	}
	b.WriteString(`p pkgs "$(dpkg-query -W -f '${Package}=${Version};' katzenpost kno 'namenlos-config-*' 'kno-host-config*' 2>/dev/null)"` + "\n")
	b.WriteString("m=\n")
	dirs := make([]string, 0, len(stateDirs))
	for _, dir := range stateDirs {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		fmt.Fprintf(&b, "k=$(df -Pk %s 2>/dev/null | awk 'NR==2{print $4}')\n", shq(dir))
		b.WriteString("if [ -n \"$k\" ] && { [ -z \"$m\" ] || [ \"$k\" -lt \"$m\" ]; }; then m=$k; fi\n")
	}
	b.WriteString("[ -n \"$m\" ] || m=$(df -Pk /var 2>/dev/null | awk 'NR==2{print $4}')\n")
	b.WriteString("p disk \"$m\"\n")
	return b.String()
}

func migrationProbe(newDirs map[string]string, client bool) string {
	var b strings.Builder
	svcs := make([]string, 0, len(newDirs))
	for svc := range newDirs {
		svcs = append(svcs, svc)
	}
	sort.Strings(svcs)
	for _, svc := range svcs {
		fmt.Fprintf(&b, "p newdir:%s \"$(ls -A %s 2>/dev/null | head -n 1)\"\n", svc, shq(newDirs[svc]))
	}
	if client {
		b.WriteString("p client \"$(systemctl is-enabled kpclientd.service 2>/dev/null || true)\"\n")
	}
	return b.String()
}

func courierProbe() string {
	return fmt.Sprintf("p courier \"old=$(test -d %s && echo yes || echo no) new=$(test -e %s && echo yes || echo no)\"\n",
		shq(service.LegacyCourierState), shq(service.CourierState))
}

func shq(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func Parse(out string) map[string]string {
	m := map[string]string{}
	for _, ln := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(ln), "=")
		if !ok || k == "" {
			continue
		}
		m[k] = strings.TrimSpace(v)
	}
	return m
}
