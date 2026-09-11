package verify

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/katzenpost/kno/internal/knopath"
)

func TestVerifyFindsTheUpstreamTools(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"kpfetch", "kpping"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	if got := FindTool("", knopath.FetchTools...); filepath.Base(got) != "kpfetch" {
		t.Errorf("fetch tool %q; the upstream package installs kpfetch", got)
	}
	if got := FindTool("", knopath.PingTools...); filepath.Base(got) != "kpping" {
		t.Errorf("ping tool %q; the upstream package installs kpping", got)
	}
}
