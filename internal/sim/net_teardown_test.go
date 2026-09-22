package sim

import (
	"errors"
	"testing"
)

func TestOnlyAPassingRunIsTornDown(t *testing.T) {
	boom := errors.New("consensus never formed")
	for _, c := range []struct {
		name string
		keep bool
		err  error
		want bool
	}{
		{"a passing run is removed", false, nil, true},
		{"unless it was asked to stay", true, nil, false},
		{"a failed run is kept, to be read", false, boom, false},
		{"and keep does not change that", true, boom, false},
	} {
		if got := tearDownAfter(c.keep, c.err); got != c.want {
			t.Errorf("%s: tearDownAfter(keep=%v, err=%v) = %v, want %v",
				c.name, c.keep, c.err, got, c.want)
		}
	}
}

func TestTheUpgradeCheckRunsAfterTheTrafficChecks(t *testing.T) {
	var order []string
	for _, s := range allStages("previous-release") {
		order = append(order, s)
	}
	idx := func(name string) int {
		for i, s := range order {
			if s == name {
				return i
			}
		}
		t.Fatalf("stage %q is not in the sequence %v", name, order)
		return -1
	}
	if idx("upgrade") < idx("message") {
		t.Errorf("upgrade runs before message: %v", order)
	}
	if idx("upgrade") < idx("probe") {
		t.Errorf("upgrade runs before probe: %v", order)
	}
	if idx("verify") > idx("message") {
		t.Errorf("verify no longer precedes message: %v", order)
	}

	found := false
	for _, s := range Stages {
		if s == "upgrade" {
			found = true
		}
	}
	if !found {
		t.Errorf("upgrade is not a runnable stage: %v", Stages)
	}
}

func TestAllRunsTheUpgradeOnlyWithAPreviousRelease(t *testing.T) {
	for _, s := range allStages("") {
		if s == "upgrade" {
			t.Fatalf("all runs upgrade with no previous release: %v", allStages(""))
		}
	}
}
