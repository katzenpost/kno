package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/track"
)

func TestTheEscalationProbeAsksSudoForTheRealCommand(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root needs no sudo")
	}
	dir := t.TempDir()
	sudo := filepath.Join(dir, "sudo")
	body := "#!/bin/sh\n[ \"$1\" = -n ] && shift\n[ \"$1\" = -l ] && shift\ncase \"$1 $2\" in */kno\\ activate) exit 0 ;; esac\nexit 1\n"
	if err := os.WriteFile(sudo, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	in, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	old := os.Stdin
	os.Stdin = in
	defer func() { os.Stdin = old; in.Close() }()
	var out strings.Builder
	r := ToolRunner{Sudo: sudo, ActivateCmd: []string{"/usr/bin/kno", "activate"},
		Log: track.Logger{Tool: "update", W: &out}, Out: &out}
	if err := r.escalation(r.activateArgv("/rel", "v0.0.2")); err != nil {
		t.Errorf("a sudo that allows kno activate refuses the timer's install: %v\n%s", err, out.String())
	}
}
