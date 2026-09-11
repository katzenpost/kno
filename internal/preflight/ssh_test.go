package preflight

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreflightUsesTheConfiguredSSH(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "called")
	fake := filepath.Join(dir, "fake-ssh")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n/usr/bin/touch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSH", fake)
	t.Setenv("PATH", dir)
	_, _ = runRemote("node.invalid", "l", "true")
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("preflight did not run $SSH; it ran some other ssh")
	}
}

func TestAnSSHThatCannotConnectIsNotAnAnswer(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "ssh")
	body := "#!/bin/sh\necho 'ssh: connect to host node.invalid port 22: Connection refused' >&2\nexit 255\n"
	if err := os.WriteFile(fake, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSH", fake)
	out, err := runRemote("node.invalid", "l", "true")
	if err == nil || !strings.Contains(err.Error(), "Connection refused") {
		t.Errorf("runRemote = %q, %v; want an error that gives the ssh message", out, err)
	}
}
