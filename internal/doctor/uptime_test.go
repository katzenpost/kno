package doctor

import (
	"strings"
	"testing"
	"time"
)

func TestUptimeFlagsAServiceYoungerThanAnEpoch(t *testing.T) {
	units := []unitReport{
		{Service: "mixy-pq-mixserver", Unit: "pq-katzenpost-mixserver", Installed: true},
		{Service: "annares-pq-authority", Unit: "pq-katzenpost-authority", Installed: true},
	}
	f := uptimeFacts{
		Booted: 3 * time.Hour,
		Started: map[string]time.Duration{
			"pq-katzenpost-mixserver": 2 * time.Minute,
			"pq-katzenpost-authority": 3 * time.Hour,
		},
	}
	out, failed := reportOnly(t, func(r *Result) { reportUptime(r, f, units) })

	if failed {
		t.Error("a freshly restarted service is not a failure; it is an explanation")
	}
	if !strings.Contains(flat(out), "started 2m ago") {
		t.Errorf("the recent restart must be named:\n%s", out)
	}
	if !strings.Contains(flat(out), "The current consensus may not include it yet") {
		t.Errorf("the report must say what the restart costs:\n%s", out)
	}

	if !strings.Contains(flat(out), "pq-katzenpost-authority running 3h0m") {
		t.Errorf("a settled service should report its uptime:\n%s", out)
	}
	if !strings.Contains(flat(out), "up 3h0m") {
		t.Errorf("host uptime is the first thing asked about a restart:\n%s", out)
	}
}

func TestUptimeSaysNothingWithoutFacts(t *testing.T) {
	out, _ := reportOnly(t, func(r *Result) {
		reportUptime(r, uptimeFacts{Started: map[string]time.Duration{}}, nil)
	})
	if strings.TrimSpace(out) != "" {
		t.Errorf("with nothing known, say nothing:\n%s", out)
	}
}

func TestShortReadsLikeADuration(t *testing.T) {
	for _, c := range []struct {
		d    time.Duration
		want string
	}{
		{45 * time.Second, "45s"},
		{90 * time.Second, "1m"},
		{90 * time.Minute, "1h30m"},
		{50 * time.Hour, "2d2h"},
	} {
		if got := short(c.d); got != c.want {
			t.Errorf("short(%s) = %q, want %q", c.d, got, c.want)
		}
	}
}

func reportOnly(t *testing.T, fn func(*Result)) (string, bool) {
	t.Helper()
	var b strings.Builder
	r := newResult(&b, true)
	fn(r)
	return b.String(), r.failed()
}
