package sim

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/katzenpost/kno/internal/probe"
	"github.com/katzenpost/kno/internal/topology"
)

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

func envStr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func (r *run) verifyStage() error {
	if err := r.installKno(); err != nil {
		return err
	}
	r.log.Logf("running 'kno verify' (generic and data plane) inside the client container")
	tries := envInt("VERIFY_RETRIES", 3)
	for i := 1; i <= tries; i++ {
		err := r.podmanStream("exec", "-e", r.epochEnv(),
			"-e", "VERIFY_GENERIC=1", "-e", "VERIFY_DATAPLANE=1",
			"-e", "VERIFY_THINCLIENT=/sim/thinclient.toml",
			"-e", "VERIFY_PING_TIMEOUT=600", "-e", "VERIFY_PING_COUNT=3",
			"-e", "KNO_FETCH="+r.tool(roleFetch),
			"-e", "KNO_PING="+r.tool(rolePing),
			"-e", "KNO_CPBENCH=/simbin/cp-bench",
			r.kpc, "/simbin/kno", "verify")
		if err == nil {
			r.log.Logf("kno verify passed (exit 0, attempt %d)", i)
			return nil
		}
		r.log.Warnf("kno verify attempt %d failed (%v); retrying", i, err)
		time.Sleep(20 * time.Second)
	}
	return fmt.Errorf("kno verify failed after %d attempts", tries)
}

var reSuccessRate = regexp.MustCompile(`(?i)Success rate`)

func attempt(budget time.Duration, args ...string) (out []byte, err error, timedOut bool) {
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	out, err = podmanCmd(ctx, args...).CombinedOutput()
	return out, err, ctx.Err() != nil
}

func budgetOf(name string, def int) time.Duration {
	return time.Duration(envInt(name, def)) * time.Second
}

type spend struct{ until time.Time }

func newSpend(total time.Duration) *spend { return &spend{until: time.Now().Add(total)} }

func (s *spend) left() (time.Duration, bool) {
	d := time.Until(s.until)
	if d < time.Second {
		return 0, false
	}
	return d, true
}

func (r *run) messageStage() error {
	if err := r.echoPing(); err != nil {
		return err
	}
	if r.NoCourier {
		r.log.Logf("pigeonhole cp-bench skipped (--no-courier)")
		return nil
	}
	return r.pigeonhole()
}

func (r *run) echoPing() error {
	count := envInt("PING_COUNT", 3)
	budget := budgetOf("PING_TIMEOUT", 300)
	want := fmt.Sprintf("100 percent (%d/%d)", count, count)
	r.log.Logf("sending an echo ping (up to %s in total)", budget)
	spend := newSpend(budget)
	for i := 1; i <= 3; i++ {
		remaining, have := spend.left()
		if !have {
			r.log.Warnf("echo ping used its %s budget after %d attempt(s)", budget, i-1)
			break
		}
		out, _, timedOut := attempt(remaining, r.pingArgs(count)...)
		if timedOut {
			r.log.Warnf("echo ping attempt %d gave no result in time; retrying", i)
			continue
		}
		for _, l := range strings.Split(string(out), "\n") {
			if reSuccessRate.MatchString(l) {
				fmt.Fprintln(r.Out, l)
			}
		}
		if strings.Contains(string(out), want) {
			r.log.Logf("echo ping 100 percent (attempt %d)", i)
			return nil
		}
		r.log.Warnf("echo ping attempt %d below 100 percent; retrying", i)
		time.Sleep(20 * time.Second)
	}
	return fmt.Errorf("echo ping did not reach 100 percent")
}

func (r *run) pigeonhole() error {
	budget := budgetOf("CPBENCH_TIMEOUT", 900)
	r.log.Logf("pigeonhole round trip with cp-bench (up to %s in total)", budget)
	logPath := filepath.Join(r.Work, "cpbench.log")
	spend := newSpend(budget)
	stalled := 0
	for i := 1; i <= 3; i++ {
		remaining, have := spend.left()
		if !have {
			stalled++
			break
		}
		out, err, timedOut := attempt(remaining, "exec", "-e", r.epochEnv(), r.kpc, "/simbin/cp-bench",
			"-config", "/sim/thinclient.toml",
			"-payload", envStr("CPBENCH_PAYLOAD", "256"),
			"-metrics", "", "-log-level", "ERROR")
		_ = os.WriteFile(logPath, out, 0o644)
		if timedOut {
			stalled++
			r.log.Warnf("cp-bench attempt %d gave no result in time; retrying", i)
			continue
		}
		if err == nil {
			for _, l := range strings.Split(string(out), "\n") {
				if strings.Contains(l, "duration=") || strings.Contains(l, "bytes_per_sec") {
					fmt.Fprintln(r.Out, l)
				}
			}
			r.log.Logf("cp-bench pigeonhole round trip passed (exit 0, attempt %d)", i)
			return nil
		}
		r.log.Warnf("cp-bench attempt %d failed; retrying", i)
	}
	if lines, err := readLines(logPath); err == nil {
		for _, l := range lines[max(0, len(lines)-5):] {
			fmt.Fprintln(r.Out, l)
		}
	}
	if stalled > 0 {
		r.dumpPigeonholeLogs()
		return fmt.Errorf("cp-bench pigeonhole round trip did not complete in %s"+
			" (see %s, and the courier and replica containers, which are still up)", budget, logPath)
	}
	return fmt.Errorf("cp-bench pigeonhole round trip failed (see %s)", logPath)
}

func (r *run) dumpPigeonholeLogs() {
	logs, err := filepath.Glob(filepath.Join(r.data, "*", "out.log"))
	if err != nil || len(logs) == 0 {
		return
	}
	sort.Strings(logs)
	for _, p := range logs {
		name := filepath.Base(filepath.Dir(p))
		if !strings.Contains(name, "courier") && !strings.Contains(name, "storagenode") &&
			!strings.Contains(name, "servicenode") {
			continue
		}
		lines, err := readLines(p)
		if err != nil {
			continue
		}
		fmt.Fprintf(r.Out, "--- %s (last 5 lines) ---\n", name)
		for _, l := range lines[max(0, len(lines)-5):] {
			fmt.Fprintln(r.Out, l)
		}
	}
}

func (r *run) knownOrphans() []string {
	known, err := topology.KnownFindings(r.Repo)
	if err != nil {
		return nil
	}
	orphans, err := topology.Orphans(r.Repo, r.Network)
	if err != nil {
		return nil
	}
	var out []string
	for _, m := range orphans {
		if topology.Known(known, topology.Finding{Subject: m}) {
			out = append(out, m)
		}
	}
	return out
}

func (r *run) probeTries() int {
	n := int((3 * r.Epoch) / (30 * time.Second))
	return max(n, envInt("PROBE_RETRIES", 3))
}

func (r *run) probeStage() error {
	if err := r.installKno(); err != nil {
		return err
	}
	orphans := strings.Join(r.knownOrphans(), " ")
	r.log.Logf("probing the sim: reachability and the consensus (known orphans: %s)", orphanNote(orphans))
	tries := r.probeTries()
	for i := 1; i <= tries; i++ {
		err := r.probeOnce()
		if err == nil {
			r.log.Logf("probe passed (attempt %d)", i)
			return nil
		}
		if exitOf(err) != probe.CodeSettling {
			return fmt.Errorf("probe found a discrepancy: %w", err)
		}
		r.log.Warnf("probe attempt %d of %d: reachable, not in consensus yet", i, tries)
		time.Sleep(30 * time.Second)
	}
	return fmt.Errorf("probe: a node is reachable but still not in consensus after %d attempts"+
		" (about %s, several epochs)", tries, time.Duration(tries)*30*time.Second)
}

func (r *run) probeOnce() error {
	return r.podmanStream(r.probeArgs()...)
}

func (r *run) probeArgs() []string {
	return []string{"exec", "-e", r.epochEnv(),
		"-e", "PROBE_KNOWN_ORPHANS=" + strings.Join(r.knownOrphans(), " "),
		r.kpc, "/simbin/kno", "probe",
		"--configs", "/sim", "--thinclient", "/sim/thinclient.toml",
		"--fetch", r.tool(roleFetch), "--timeout", "5"}
}

func orphanNote(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func exitOf(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}
