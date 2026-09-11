package update

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/katzenpost/kno/internal/testutil"
	"github.com/katzenpost/kno/internal/track"
)

func kvAt(activated, deadline time.Time, verdict string) map[string]string {
	return map[string]string{"tag": "v0.0.107",
		"activated": strconv.FormatInt(activated.Unix(), 10),
		"deadline":  strconv.FormatInt(deadline.Unix(), 10),
		"verdict":   verdict}
}

func TestTheNextCheckComesAtTheRightTime(t *testing.T) {
	t.Setenv("KATZENPOST_EPOCH_DURATION", "20m")
	t.Setenv("KNO_POLL_SECS", "")
	now := time.Unix(1792000000, 0)
	act := now.Add(-10 * time.Minute)
	end := act.Add(90 * time.Minute)
	if got := pollEvery(); got != 5*time.Minute {
		t.Errorf("poll interval with 20 min epochs = %s, want 5m", got)
	}
	if next, ok := nextCheck(kvAt(act, end, "1"), now); !ok || !next.Equal(now.Add(5*time.Minute)) {
		t.Errorf("not yet: next = %s %v, want in 5 min", next, ok)
	}
	near := act.Add(38 * time.Minute)
	floor := act.Add(40 * time.Minute)
	if next, ok := nextCheck(kvAt(act, end, "0"), near); !ok || !next.Equal(floor.Add(time.Second)) {
		t.Errorf("passed before the floor: next = %s, want just after the floor %s", next, floor)
	}
	late := end.Add(-time.Minute)
	if next, ok := nextCheck(kvAt(act, end, "1"), late); !ok || !next.Equal(end.Add(time.Second)) {
		t.Errorf("near the deadline: next = %s, want just after %s", next, end)
	}
	if _, ok := nextCheck(kvAt(act, end, strconv.Itoa(VerifyUnavailable)), end.Add(time.Minute)); ok {
		t.Error("an unreachable host past its deadline is waited on forever")
	}
	if next, ok := nextCheck(kvAt(act, end, strconv.Itoa(VerifyUnavailable)), now); !ok || next.IsZero() {
		t.Error("an ssh failure before the deadline stopped the wait")
	}
	t.Setenv("KATZENPOST_EPOCH_DURATION", "1m")
	if got := pollEvery(); got != 15*time.Second {
		t.Errorf("poll interval with 1 min epochs = %s, want 15s", got)
	}
	t.Setenv("KATZENPOST_EPOCH_DURATION", "1h")
	if got := pollEvery(); got != 5*time.Minute {
		t.Errorf("poll interval with 1 h epochs = %s, want 5m, so the shared ssh connection stays open", got)
	}
	t.Setenv("KATZENPOST_EPOCH_DURATION", "1ms")
	if got := pollEvery(); got != 10*time.Second {
		t.Errorf("poll interval floor = %s, want 10s", got)
	}
}

func waitTicker(t *testing.T, state, pending string) (*ticker, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	e := track.Env{StateDir: dir}
	var out bytes.Buffer
	tk := newTicker(&out, track.Logger{Tool: "update", W: &out}, e, Flags{},
		remoteHost{Label: "annares", Node: "root@power9-internal"}, nil, "full", "v0.0.107")
	if err := os.MkdirAll(tk.he.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	put := func(name, body string) {
		if err := os.WriteFile(tk.stateFile(name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put("state", state+"\n")
	if pending != "" {
		put("pending", pending)
	}
	tk.he.CurrentFile = tk.stateFile("current")
	tk.he.QuarantineFile = tk.stateFile("quarantine")
	return tk, &out
}

func TestTheWaitTicksUntilTheHostSettles(t *testing.T) {
	now := time.Unix(1792000000, 0)
	pending := fmt.Sprintf("tag=v0.0.107\nactivated=%d\ndeadline=%d\n", now.Unix(), now.Add(90*time.Minute).Unix())
	tk, out := waitTicker(t, "pending", pending)
	clock := now
	tk.now = func() time.Time { return clock }
	var slept []time.Duration
	tk.sleep = func(d time.Duration) { slept = append(slept, d); clock = clock.Add(d) }
	ticks := 0
	tk.step = func(first bool) int {
		if first {
			t.Error("a tick inside the wait ran as the first tick, so --force would repeat")
		}
		ticks++
		if ticks == 3 {
			for name, body := range map[string]string{"state": "steady\n", "current": "v0.0.107\n",
				"last-known-good": "v0.0.107\n"} {
				if err := os.WriteFile(tk.stateFile(name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			os.Remove(tk.stateFile("pending"))
		}
		return 0
	}
	tk.wait()
	if ticks != 3 || len(slept) != 3 {
		t.Fatalf("ticks = %d, sleeps = %v, want 3 of each", ticks, slept)
	}
	if !strings.Contains(out.String(), "annares: verifying v0.0.107") {
		t.Errorf("the wait printed no progress:\n%s", out.String())
	}
	if what, rc := tk.outcome(); rc != exitDone || !strings.Contains(what, "last good release") {
		t.Errorf("outcome = %q %d", what, rc)
	}
}

func TestTheWaitStopsAtAHold(t *testing.T) {
	tk, _ := waitTicker(t, "pending", "tag=v0.0.107\ndeadline=1\nheld=1\nverdict=4\n")
	tk.step = func(bool) int { t.Error("a held host was ticked"); return 0 }
	tk.wait()
	what, rc := tk.outcome()
	if rc != exitUnsettled || !strings.Contains(what, "held") || !strings.Contains(what, "cannot ask") {
		t.Errorf("outcome = %q %d", what, rc)
	}
}

func TestTheOutcomeOfARollback(t *testing.T) {
	tk, _ := waitTicker(t, "steady", "")
	os.WriteFile(tk.stateFile("current"), []byte("v0.0.104\n"), 0o644)
	os.WriteFile(tk.stateFile("quarantine"), []byte("v0.0.107\n"), 0o644)
	what, rc := tk.outcome()
	if rc != exitFailed || !strings.Contains(what, "runs v0.0.104 again") ||
		!strings.Contains(what, "kno unquarantine --host annares v0.0.107") {
		t.Errorf("outcome = %q %d", what, rc)
	}
}

func TestEveryHostRunsAndAFailedOneSetsTheExitStatus(t *testing.T) {
	saved := makeTicker
	defer func() { makeTicker = saved }()
	base := t.TempDir()
	var mu sync.Mutex
	var started []string
	makeTicker = func(out io.Writer, l track.Logger, e track.Env, v Flags, h remoteHost,
		st *stage, mode, target string) *ticker {
		tk := newTicker(out, l, track.Env{StateDir: base}, v, h, st, mode, target)
		os.MkdirAll(tk.he.StateDir, 0o755)
		tk.he.CurrentFile = tk.stateFile("current")
		tk.he.QuarantineFile = tk.stateFile("quarantine")
		tk.sleep = func(time.Duration) {}
		tk.step = func(first bool) int {
			if h.Label == "b" && first {
				mu.Lock()
				started = append(started, h.Label)
				mu.Unlock()
				os.WriteFile(tk.stateFile("state"), []byte("pending\n"), 0o644)
				os.WriteFile(tk.stateFile("pending"), []byte(fmt.Sprintf(
					"tag=v0.0.107\ndeadline=%d\n", time.Now().Add(time.Hour).Unix())), 0o644)
				return 0
			}
			cur := "v0.0.107"
			if h.Label == "b" {
				cur = "v0.0.104"
				os.WriteFile(tk.he.QuarantineFile, []byte("v0.0.107\n"), 0o644)
				os.Remove(tk.stateFile("pending"))
			} else {
				mu.Lock()
				started = append(started, h.Label)
				mu.Unlock()
			}
			os.WriteFile(tk.stateFile("state"), []byte("steady\n"), 0o644)
			os.WriteFile(tk.stateFile("current"), []byte(cur+"\n"), 0o644)
			os.WriteFile(tk.stateFile("last-known-good"), []byte(cur+"\n"), 0o644)
			return 0
		}
		return tk
	}
	var out bytes.Buffer
	l := track.Logger{Tool: "update", W: &out}
	hosts := []remoteHost{{Label: "a"}, {Label: "b"}, {Label: "c"}}
	rc := waitHosts(&out, l, track.Env{StateDir: base}, Flags{}, hosts, nil, "full", "v0.0.107")
	if rc != exitFailed {
		t.Errorf("rc = %d, want %d", rc, exitFailed)
	}
	sort.Strings(started)
	if strings.Join(started, " ") != "a b c" {
		t.Errorf("hosts started = %v, want a b c", started)
	}
	if !strings.Contains(out.String(), "b: runs v0.0.104 again") {
		t.Errorf("the output does not say that b failed:\n%s", out.String())
	}
}

func TestAGroupGoesOnPastAHostThatIsQuarantinedFromBefore(t *testing.T) {
	saved := makeTicker
	defer func() { makeTicker = saved }()
	base := t.TempDir()
	var mu sync.Mutex
	var started []string
	makeTicker = func(out io.Writer, l track.Logger, e track.Env, v Flags, h remoteHost,
		st *stage, mode, target string) *ticker {
		tk := newTicker(out, l, track.Env{StateDir: base}, v, h, st, mode, target)
		os.MkdirAll(tk.he.StateDir, 0o755)
		tk.he.CurrentFile = tk.stateFile("current")
		tk.he.QuarantineFile = tk.stateFile("quarantine")
		cur := "v0.0.107"
		if h.Label == "a" {
			cur = "v0.0.104"
			os.WriteFile(tk.he.QuarantineFile, []byte("v0.0.107\n"), 0o644)
		}
		for n, b := range map[string]string{"state": "steady\n", "current": cur + "\n",
			"last-known-good": cur + "\n"} {
			os.WriteFile(tk.stateFile(n), []byte(b), 0o644)
		}
		tk.step = func(bool) int { mu.Lock(); started = append(started, h.Label); mu.Unlock(); return 0 }
		return tk
	}
	var out bytes.Buffer
	hosts := []remoteHost{{Label: "a"}, {Label: "b"}}
	rc := waitHosts(&out, track.Logger{Tool: "update", W: &out}, track.Env{StateDir: base}, Flags{},
		hosts, nil, "full", "v0.0.107")
	sort.Strings(started)
	if rc != exitFailed || strings.Join(started, " ") != "a b" {
		t.Errorf("rc = %d, started = %v; want 1 and both hosts", rc, started)
	}
	if !strings.Contains(out.String(), "kno installed nothing on a") {
		t.Errorf("the output does not say that a was left as it was:\n%s", out.String())
	}
}

func TestAHostThatRunsTheTargetWithoutAPromotionFailed(t *testing.T) {
	for _, lkg := range []string{"", "v0.0.104\n"} {
		tk, _ := waitTicker(t, "steady", "")
		os.WriteFile(tk.stateFile("current"), []byte("v0.0.107\n"), 0o644)
		if lkg != "" {
			os.WriteFile(tk.stateFile("last-known-good"), []byte(lkg), 0o644)
		}
		what, rc := tk.outcome()
		if rc != exitFailed || !strings.Contains(what, "did not promote") {
			t.Errorf("last good %q: outcome = %q %d, want exit %d", lkg, what, rc, exitFailed)
		}
	}
}

func TestAHostWhoseNewConfigsFailedVerifyFailed(t *testing.T) {
	tk, _ := waitTicker(t, "steady", "")
	tk.he.Repo, tk.he.Network = testutil.RepoRoot(t), "namenlos"
	tk.h.Services = []string{"mixy-pq-mixserver"}
	want := configsOf(tk.he.Repo, tk.he.Network, tk.h.Services)
	for n, b := range map[string]string{"current": "v0.0.107\n", "last-known-good": "v0.0.107\n",
		"configs": want + "\n", "configs-failed": want + "\n"} {
		os.WriteFile(tk.stateFile(n), []byte(b), 0o644)
	}
	if what, rc := tk.outcome(); rc != exitFailed || !strings.Contains(what, "did not pass verify") {
		t.Errorf("the new configs stay installed: outcome = %q %d, want exit %d", what, rc, exitFailed)
	}
	os.WriteFile(tk.stateFile("configs"), []byte("mixy-pq-mixserver=+1\n"), 0o644)
	if what, rc := tk.outcome(); rc != exitFailed {
		t.Errorf("the earlier configs run again: outcome = %q %d, want exit %d", what, rc, exitFailed)
	}
	os.WriteFile(tk.stateFile("configs-failed"), []byte("mixy-pq-mixserver=+0\n"), 0o644)
	if what, rc := tk.outcome(); rc != exitDone {
		t.Errorf("configs that failed before a commit changed them: outcome = %q %d, want exit 0", what, rc)
	}
}

func TestAFailedHostDecidesTheExitStatusInEitherOrder(t *testing.T) {
	saved := makeTicker
	defer func() { makeTicker = saved }()
	for _, order := range [][]remoteHost{
		{{Label: "a"}, {Label: "b"}},
		{{Label: "b"}, {Label: "a"}},
	} {
		base := t.TempDir()
		e := track.Env{StateDir: base, Repo: testutil.RepoRoot(t), Network: "namenlos"}
		makeTicker = func(out io.Writer, l track.Logger, e track.Env, v Flags, h remoteHost,
			st *stage, mode, target string) *ticker {
			tk := newTicker(out, l, e, v, h, st, mode, target)
			os.MkdirAll(tk.he.StateDir, 0o755)
			state := map[string]string{"state": "steady\n", "current": "v0.0.107\n",
				"last-known-good": "v0.0.107\n"}
			if h.Label == "a" {
				state = map[string]string{"state": "steady\n", "current": "v0.0.104\n",
					"last-known-good": "v0.0.104\n", "quarantine": "v0.0.107\n"}
			}
			for n, b := range state {
				os.WriteFile(tk.stateFile(n), []byte(b), 0o644)
			}
			tk.step = func(bool) int { return 0 }
			return tk
		}
		var out bytes.Buffer
		rc := waitHosts(&out, track.Logger{Tool: "update", W: &out}, e, Flags{}, order, nil, "full", "v0.0.107")
		if rc != exitFailed {
			t.Errorf("hosts %s %s: rc = %d, want %d\n%s", order[0].Label, order[1].Label, rc, exitFailed, out.String())
		}
	}
}

func TestTheWaitEndsWhenARollbackFailsAfterTheWindow(t *testing.T) {
	now := time.Unix(1792000000, 0)
	tk, _ := waitTicker(t, "pending", fmt.Sprintf("tag=v0.0.107\nprev=v0.0.104\ndeadline=%d\nverdict=2\n",
		now.Add(-time.Minute).Unix()))
	clock := now
	tk.now = func() time.Time { return clock }
	tk.sleep = func(d time.Duration) { clock = clock.Add(d) }
	ticks := 0
	tk.step = func(bool) int {
		ticks++
		if ticks > 3 {
			t.Fatal("the wait repeats a rollback that fails, without end")
		}
		return 1
	}
	tk.wait()
	if ticks != 1 {
		t.Errorf("ticks = %d, want 1", ticks)
	}
	if what, rc := tk.outcome(); rc != exitFailed || !strings.Contains(what, "could not roll it back") {
		t.Errorf("outcome = %q %d, want exit %d", what, rc, exitFailed)
	}
}
