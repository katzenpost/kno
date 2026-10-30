package update

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/track"
)

func TestSuActivationKeepsTheTrustedRepositoryAndConfigArguments(t *testing.T) {
	r := ToolRunner{Sudo: "su", ActivateCmd: []string{"/usr/bin/kno", "activate"},
		Env: track.Env{Repo: "/home/operator/repo/data", ConfigPath: "/home/operator/kno.toml"}}
	argv := r.activateArgv("/home/operator/release", "v0.0.107")
	if len(argv) != 4 || argv[0] != "su" || argv[1] != "root" || argv[2] != "-c" {
		t.Fatalf("su activation: %v", argv)
	}
	for _, want := range []string{"'/usr/bin/kno'", "'activate'", "'--repo=" + r.Env.Repo + "'", "'--config=" + r.Env.ConfigPath + "'", "'/home/operator/release'", "'v0.0.107'"} {
		if !strings.Contains(argv[3], want) {
			t.Fatalf("su activation lost %s: %v", want, argv)
		}
	}
}

func TestSuDoesNotRunASudoProbeWithoutATerminal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root needs no escalation")
	}
	dir := t.TempDir()
	helper, marker := filepath.Join(dir, "su"), filepath.Join(dir, "called")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	in, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	writer.Close()
	old := os.Stdin
	os.Stdin = in
	t.Cleanup(func() { os.Stdin = old; in.Close() })
	var out strings.Builder
	r := ToolRunner{Sudo: helper, ActivateCmd: []string{"/usr/bin/kno", "activate"},
		Log: track.Logger{Tool: "update", W: &out}, Out: &out}
	if err := r.escalation(r.activateArgv("/release", "v0.0.107")); !errors.Is(err, ErrNoEscalation) {
		t.Fatalf("noninteractive su: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("su received an invalid sudo-style probe")
	}
}
