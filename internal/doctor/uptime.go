package doctor

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const epoch = 20 * time.Minute

type uptimeFacts struct {
	Booted time.Duration

	Started map[string]time.Duration
}

func gatherUptime(units []unitReport) uptimeFacts {
	f := uptimeFacts{Started: map[string]time.Duration{}}
	f.Booted = sinceBoot()
	for _, u := range units {
		if !u.Installed {
			continue
		}
		if d, ok := sinceUnitStart(u.Unit); ok {
			f.Started[u.Unit] = d
		}
	}
	return f
}

func sinceBoot() time.Duration {
	out, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(out))
	if len(f) == 0 {
		return 0
	}
	secs, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0
	}
	return time.Duration(secs) * time.Second
}

func sinceUnitStart(unit string) (time.Duration, bool) {
	v := showUnit(unit, "ActiveEnterTimestampMonotonic")
	if v == "" || v == "0" {
		return 0, false
	}
	usec, err := strconv.ParseInt(v, 10, 64)
	if err != nil || usec == 0 {
		return 0, false
	}
	now := monotonicNow()
	if now == 0 || now < usec {
		return 0, false
	}
	return time.Duration(now-usec) * time.Microsecond, true
}

func monotonicNow() int64 {
	d := sinceBoot()
	if d == 0 {
		return 0
	}
	return int64(d / time.Microsecond)
}

func reportUptime(r *Result, f uptimeFacts, units []unitReport) {
	if f.Booted == 0 && len(f.Started) == 0 {
		return
	}
	if f.Booted > 0 {
		r.check("host uptime", true, "up %s", short(f.Booted))
	}
	for _, u := range units {
		d, ok := f.Started[u.Unit]
		if !ok {
			continue
		}
		if d >= epoch {
			r.check(u.Service+" uptime", true, "%s running %s", u.Unit, short(d))
			continue
		}

		r.warn(u.Service+" uptime", "%s started %s ago. That is less than one epoch (%s)."+
			" The current consensus may not include it yet. This is normal"+
			" until the next vote.", u.Unit, short(d), short(epoch))
	}
}

func short(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
}
