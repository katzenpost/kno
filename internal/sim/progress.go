package sim

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type transferOutput struct {
	mu   sync.Mutex
	data bytes.Buffer
	line string
	last time.Time
}

func (o *transferOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.data.Write(p)
	o.line += string(p)
	for {
		line, rest, ok := strings.Cut(o.line, "\n")
		if !ok {
			break
		}
		o.line = rest
		if strings.Contains(line, "Write operation complete") ||
			strings.Contains(line, "Copy command completed successfully") ||
			strings.Contains(line, "Sending request (isRead=true,") {
			o.last = time.Now()
		}
	}
	return len(p), nil
}

func (o *transferOutput) idle(now time.Time) time.Duration {
	o.mu.Lock()
	defer o.mu.Unlock()
	return now.Sub(o.last)
}

func transferAttempt(ctx context.Context, idle time.Duration, cmd *exec.Cmd, stop func()) ([]byte, error, bool) {
	out := &transferOutput{last: time.Now()}
	cmd.Stdout, cmd.Stderr = out, out
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		return nil, err, false
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	tick := time.NewTicker(min(time.Second, idle/4))
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			return out.data.Bytes(), err, false
		case <-ctx.Done():
			stop()
			_ = cmd.Process.Kill()
			err := <-done
			return out.data.Bytes(), err, true
		case now := <-tick.C:
			if out.idle(now) >= idle {
				stop()
				_ = cmd.Process.Kill()
				err := <-done
				return out.data.Bytes(), err, true
			}
		}
	}
}
