package sim

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestAnAttemptIsBounded(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no shell")
	}

	name := fmt.Sprintf("kno-attempt-test-%d", os.Getpid())
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", name).Run()
	})
	start := time.Now()
	_, _, timedOut := attempt(300*time.Millisecond, "run", "--rm",
		"--name", name, "docker.io/library/alpine", "sleep", "60")
	elapsed := time.Since(start)
	if !timedOut {
		t.Skip("podman returned before the deadline (not installed, or very fast failure)")
	}
	if elapsed > 30*time.Second {
		t.Errorf("the deadline did not bound the attempt: took %s", elapsed)
	}
}

func TestABudgetAlwaysHasADeadline(t *testing.T) {
	t.Setenv("KNO_TEST_BUDGET", "")
	if got := budgetOf("KNO_TEST_BUDGET", 600); got != 600*time.Second {
		t.Errorf("unset budget = %s, want 10m", got)
	}
	t.Setenv("KNO_TEST_BUDGET", "5")
	if got := budgetOf("KNO_TEST_BUDGET", 600); got != 5*time.Second {
		t.Errorf("overridden budget = %s, want 5s", got)
	}

	t.Setenv("KNO_TEST_BUDGET", "soon")
	if got := budgetOf("KNO_TEST_BUDGET", 600); got != 600*time.Second {
		t.Errorf("unparsable budget = %s, want the default", got)
	}
	if got := budgetOf("KNO_TEST_BUDGET_UNSET", 600); got <= 0 {
		t.Error("a budget of zero or less would time out every attempt instantly")
	}
}

func TestTheBudgetIsSpentAcrossAttempts(t *testing.T) {

	s := newSpend(1500 * time.Millisecond)
	first, ok := s.left()
	if !ok || first <= 0 {
		t.Fatal("a fresh budget has nothing to spend")
	}
	if first > 1500*time.Millisecond {
		t.Errorf("the first attempt was offered %s of a 1.5s budget", first)
	}
	time.Sleep(1600 * time.Millisecond)
	if _, ok := s.left(); ok {
		t.Error("the budget still had time to give after it had run out")
	}
}

func TestASliverOfBudgetIsNotOffered(t *testing.T) {
	s := &spend{until: time.Now().Add(50 * time.Millisecond)}
	if _, ok := s.left(); ok {
		t.Error("50ms was offered as an attempt budget")
	}
}
