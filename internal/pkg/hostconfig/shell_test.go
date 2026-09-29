package hostconfig

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

func TestHostConfigDebMatchesShell(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not available")
	}
	repo := testutil.RepoRoot(t)
	tmp := t.TempDir()
	network := "namenlos"
	ver := "0.0.104"

	knoBin := filepath.Join(tmp, "kno")
	build := exec.Command("go", "build", "-o", knoBin, "./cmd/kno")
	build.Dir = testutil.ModuleRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/kno: %v\n%s", err, out)
	}

	cap := filepath.Join(tmp, "cap")
	if err := os.MkdirAll(cap, 0o755); err != nil {
		t.Fatal(err)
	}
	wrap := filepath.Join(tmp, "capture.sh")
	if err := os.WriteFile(wrap, []byte("#!/bin/sh\ncp -a \"$1\" \"$CAP/\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	shOut := filepath.Join(tmp, "shout")
	cmd := exec.Command("sh", filepath.Join(testutil.ModuleRoot(t), "scripts", "host-config-build.sh"),
		network, ver, shOut)
	cmd.Env = append(os.Environ(),
		"KNO_REPO="+repo, "KNO_BIN="+knoBin, "DEB_BUILD="+wrap, "CAP="+cap,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("shell host-config-build.sh: %v\n%s", err, stderr.String())
	}

	goRoot, err := AssembleRoot(repo, network, ver, filepath.Join(tmp, "goroot"))
	if err != nil {
		t.Fatalf("AssembleRoot: %v", err)
	}

	shRoot := filepath.Join(cap, ".pkgroot")
	want := testutil.TreeSnapshot(t, shRoot)
	got := testutil.TreeSnapshot(t, goRoot)
	var wk, gk []string
	for k := range want {
		wk = append(wk, k)
	}
	for k := range got {
		gk = append(gk, k)
	}
	sort.Strings(wk)
	sort.Strings(gk)
	if strings.Join(wk, ",") != strings.Join(gk, ",") {
		t.Fatalf("file list differs:\n  shell: %v\n  go:    %v", wk, gk)
	}
	if len(wk) == 0 {
		t.Fatal("no files captured from the shell root")
	}
	for _, k := range wk {
		if want[k] != got[k] {
			t.Errorf("file %s differs:\n  shell:\n%q\n  go:\n%q", k, want[k], got[k])
		}
	}

	lines := strings.Fields(strings.TrimSpace(string(stdout)))
	if len(lines) == 0 {
		t.Fatal("shell printed no .deb path")
	}
	shDeb := filepath.Base(lines[len(lines)-1])
	if goDeb := "kno-host-config_" + ver + "_all.deb"; shDeb != goDeb {
		t.Errorf(".deb name differs: shell %s, go %s", shDeb, goDeb)
	}
	if !t.Failed() {
		t.Logf("Go kno-host-config root matches shell (%d files)", len(wk))
	}
}
