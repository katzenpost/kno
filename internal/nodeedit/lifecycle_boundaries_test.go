package nodeedit

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/katzenpost/kno/internal/testutil"
)

func TestLifecycleAllowsResolvingAnExistingTopologyWarning(t *testing.T) {
	repo := lifecycleRepo(t)
	plan, err := PlanLifecycle(repo, "namenlos", []LifecycleOperation{{Action: LifecycleMoveMix, Node: "wauland2", Layer: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyLifecycle(plan); err != nil {
		t.Fatalf("a valid topology improvement was rejected: %v", err)
	}
}

func TestLifecycleRejectsNonregularSourceWithoutBlocking(t *testing.T) {
	repo := lifecycleRepo(t)
	pipe := filepath.Join(repo, "networks", "namenlos", "unexpected.pipe")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := PlanLifecycle(repo, "namenlos", []LifecycleOperation{{Action: LifecycleMoveMix, Node: "wauland2", Layer: 2}})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "regular") {
			t.Fatalf("nonregular source returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("planning blocked while reading a nonregular source")
	}
}

func TestLifecycleRejectsPrivateMaterialInSourceComments(t *testing.T) {
	repo := lifecycleRepo(t)
	net := filepath.Join(repo, "networks", "namenlos")
	before := testutil.TreeSnapshot(t, net)
	body, err := os.ReadFile(filepath.Join(net, "editable-configs", "nodes", "lilly-servicenode.toml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body) + "\n# -----BEGIN PRIVATE KEY-----\n# test-only-secret-material\n# -----END PRIVATE KEY-----\n"
	plan, err := PlanCandidate(repo, "namenlos", []CandidateEdit{{Path: "nodes/lilly-servicenode.toml", Base: string(body), Text: text}})
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyLifecycle(plan); err == nil || !strings.Contains(err.Error(), "private key") {
		t.Fatalf("source private key guard returned %v", err)
	}
	if !equalSnapshot(before, testutil.TreeSnapshot(t, net)) {
		t.Fatal("private source candidate changed the published network")
	}
}
