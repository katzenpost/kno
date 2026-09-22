package sim

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakePodman(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "podman")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PODMAN", p)
	return dir
}

func TestDownReportsAPodmanFailure(t *testing.T) {
	fakePodman(t, "case \"$1\" in ps) echo abc123 ;; rm) echo 'Error: container is busy' >&2; exit 125 ;; esac\n")
	var log strings.Builder
	r := newRun(NetOptions{Net: "dn", Work: t.TempDir(), Out: &log})
	if err := r.downStage(); err == nil {
		t.Fatal("down passed though podman could not remove a container")
	}
	if strings.Contains(log.String(), "network dn removed") {
		t.Errorf("down said it removed the network:\n%s", log.String())
	}
}

func TestDownAcceptsANetworkThatIsGoneAlready(t *testing.T) {
	fakePodman(t, "case \"$1\" in network) echo 'Error: unable to find network with name or ID dn: network not found' >&2; exit 125 ;; esac\n")
	r := newRun(NetOptions{Net: "dn", Work: t.TempDir(), Out: &strings.Builder{}})
	if err := r.downStage(); err != nil {
		t.Fatalf("down failed on a network that was gone already: %v", err)
	}
}

func TestEmulationNeedsAContainerThatRuns(t *testing.T) {
	fakePodman(t, "echo 'Error: short-name resolution failed' >&2; exit 125\n")
	r := newRun(NetOptions{Net: "em", Work: t.TempDir(), Out: &strings.Builder{}})
	if r.emulates(platform{Goarch: "arm64", OS: "debian-13"}) {
		t.Error("a podman run that failed counted as working emulation")
	}
}
