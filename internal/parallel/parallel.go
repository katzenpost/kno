package parallel

import (
	"context"
	"os"
	"runtime"
	"strconv"

	"golang.org/x/sync/errgroup"
)

type Task func(context.Context) error

func Jobs() int {
	if v := os.Getenv("KNO_BUILD_JOBS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return runtime.NumCPU()
}

func GOMAXPROCS(jobs, tasks int) int {
	inflight := jobs
	if tasks > 0 && tasks < inflight {
		inflight = tasks
	}
	if inflight < 1 {
		inflight = 1
	}
	n := runtime.NumCPU() / inflight
	if n < 1 {
		n = 1
	}
	return n
}

func Run(limit int, tasks []Task) error {
	return RunContext(context.Background(), limit, tasks)
}

func RunContext(parent context.Context, limit int, tasks []Task) error {
	if limit < 1 {
		limit = 1
	}
	g, ctx := errgroup.WithContext(parent)
	g.SetLimit(limit)
	for _, t := range tasks {
		t := t
		g.Go(func() error { return t(ctx) })
	}
	return g.Wait()
}
