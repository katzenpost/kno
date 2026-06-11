package cli

import (
	"os"
	"testing"
)

func TestRootNeedsNoSudo(t *testing.T) {
	t.Setenv("SUDO", "")
	if err := os.Unsetenv("SUDO"); err != nil {
		t.Fatal(err)
	}
	if got := sudoFor(0); got != "" {
		t.Errorf("as root: %q, want none; FreeBSD base has no sudo", got)
	}
	if got := sudoFor(1000); got != "sudo" {
		t.Errorf("as a user: %q, want sudo", got)
	}
	t.Setenv("SUDO", "doas")
	if got := sudoFor(0); got != "doas" {
		t.Errorf("SUDO=doas as root: %q, want doas", got)
	}
}
