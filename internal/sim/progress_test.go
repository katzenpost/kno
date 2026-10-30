package sim

import (
	"context"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func transferProcess(script string) (*exec.Cmd, func()) {
	cmd := exec.Command("sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stop := func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	return cmd, stop
}

func TestTransferProgressResetsInactivityButNotForPKINoise(t *testing.T) {
	old := time.Now().Add(-time.Hour)
	out := &transferOutput{last: old}
	_, _ = out.Write([]byte("Cached PKI document for epoch 1\n"))
	if !out.last.Equal(old) {
		t.Fatal("PKI refresh counted as transfer progress")
	}
	_, _ = out.Write([]byte("StartResendingEncryptedMessage: Write operation "))
	if !out.last.Equal(old) {
		t.Fatal("an incomplete log line counted as progress")
	}
	_, _ = out.Write([]byte("complete\n"))
	if !out.last.After(old) {
		t.Fatal("completed write did not count as progress")
	}
	progress := out.last
	_, _ = out.Write([]byte("Cached PKI document for epoch 2\n"))
	if !out.last.Equal(progress) {
		t.Fatal("PKI noise extended the transfer")
	}
}

func TestTransferCanFinishAfterItsInitialInactivityAllowance(t *testing.T) {
	cmd, stop := transferProcess(`
printf 'StartResendingEncryptedMessage: Write operation complete\n'
sleep 0.4
printf 'StartResendingCopyCommand: Copy command completed successfully\n'
sleep 0.4
printf 'StartResendingEncryptedMessage: Sending request (isRead=true, replyIndex=0)\n'
sleep 0.4
printf 'cp-bench: payload=256 chunks=2 duration=1.2s bytes_per_sec=213.3\n'
`)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	out, err, timedOut := transferAttempt(ctx, time.Second, cmd, stop)
	if err != nil || timedOut || !cpbenchResult(string(out), "256") {
		t.Fatalf("healthy write/copy/read transfer was interrupted: %s (%v, timeout=%v)", out, err, timedOut)
	}
}

func TestTransferProgressCannotExtendTheTotalDeadline(t *testing.T) {
	cmd, stop := transferProcess(`while :; do printf 'Write operation complete\n'; sleep 0.02; done`)
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	out, err, timedOut := transferAttempt(ctx, time.Second, cmd, stop)
	if err == nil || !timedOut || ctx.Err() == nil || cpbenchResult(string(out), "256") {
		t.Fatalf("progress bypassed the total deadline: %s (%v, timeout=%v)", out, err, timedOut)
	}
}

func TestTransferWithoutProgressStopsEvenWithPKIRefreshes(t *testing.T) {
	cmd, stop := transferProcess(`while :; do printf 'Cached PKI document for epoch 1\n'; sleep 0.02; done`)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	out, err, timedOut := transferAttempt(ctx, 200*time.Millisecond, cmd, stop)
	if err == nil || !timedOut || ctx.Err() != nil || !strings.Contains(string(out), "Cached PKI") {
		t.Fatalf("stalled transfer did not stop on inactivity: %s (%v, timeout=%v)", out, err, timedOut)
	}
}
