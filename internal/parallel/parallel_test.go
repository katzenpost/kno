package parallel

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

func TestRunCancelsSiblings(t *testing.T) {
	boom := errors.New("boom")
	var started, observedCancel atomic.Int32

	fail := func(context.Context) error { return boom }
	watch := func(ctx context.Context) error {
		started.Add(1)
		<-ctx.Done()
		observedCancel.Add(1)
		return nil
	}

	tasks := []Task{watch, fail, watch}
	if err := Run(3, tasks); !errors.Is(err, boom) {
		t.Fatalf("expected the task error back, got %v", err)
	}
	if got := observedCancel.Load(); got != started.Load() {
		t.Errorf("every started sibling should observe cancellation: %d of %d",
			got, started.Load())
	}
}

func TestRunLaterTasksSeeCancelledContext(t *testing.T) {
	boom := errors.New("boom")
	var ran, sawCancel atomic.Int32
	later := func(ctx context.Context) error {
		ran.Add(1)
		if ctx.Err() != nil {
			sawCancel.Add(1)
		}
		return nil
	}
	tasks := []Task{
		func(context.Context) error { return boom },
		later, later,
	}
	if err := Run(1, tasks); !errors.Is(err, boom) {
		t.Fatalf("expected boom, got %v", err)
	}
	if ran.Load() != 2 {
		t.Fatalf("errgroup should still invoke queued tasks, ran %d", ran.Load())
	}
	if sawCancel.Load() != 2 {
		t.Errorf("tasks after a failure should see a cancelled context: %d of 2",
			sawCancel.Load())
	}
}
