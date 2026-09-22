package sim

import (
	"strings"
	"testing"
	"time"
)

func TestTheEpochHasAFloor(t *testing.T) {
	for _, d := range []time.Duration{0, 90 * time.Second, 2*time.Minute - time.Second} {
		if CheckEpoch(d) == nil {
			t.Errorf("epoch %s accepted; katzenpost refuses anything under %s", d, MinEpoch)
		}
	}
	if CheckEpoch(2*time.Minute+500*time.Millisecond) == nil {
		t.Error("an epoch that is not whole seconds was accepted")
	}
	for _, d := range []time.Duration{2 * time.Minute, 5 * time.Minute, 20 * time.Minute} {
		if err := CheckEpoch(d); err != nil {
			t.Errorf("epoch %s refused: %v", d, err)
		}
	}
}

func TestEveryDaemonAndClientGetsTheEpoch(t *testing.T) {
	r := newRun(NetOptions{Net: "t", Work: t.TempDir(), Epoch: 3 * time.Minute})
	want := "KATZENPOST_EPOCH_DURATION=3m0s"
	if got := r.epochEnv(); got != want {
		t.Fatalf("epochEnv = %q, want %q", got, want)
	}
	for name, args := range map[string][]string{
		"fetch": r.fetchArgs(),
		"ping":  r.pingArgs(1),
	} {
		if !strings.Contains(strings.Join(args, " "), "-e "+want) {
			t.Errorf("%s runs without the epoch: %v", name, args)
		}
	}
}

func TestTheDefaultEpochIsTheShortestAllowed(t *testing.T) {
	r := newRun(NetOptions{Net: "t", Work: t.TempDir()})
	if r.Epoch != MinEpoch {
		t.Errorf("default epoch %s, want %s", r.Epoch, MinEpoch)
	}
}
