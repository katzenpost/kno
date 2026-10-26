package push

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAStalledHostCannotHangThePushProbe(t *testing.T) {
	ssh := filepath.Join(t.TempDir(), "ssh")
	if err := os.WriteFile(ssh, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		Probe(context.Background(), Options{SSH: ssh, Node: "h", Label: "h"}, func(string, ...interface{}) {})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("a host that accepts ssh and never answers holds kno push's probe for more than 5 s")
	}
}
