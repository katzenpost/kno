package push

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/build"
)

func TestWindfallSuBootstrapChecksDigestsBeforeInstalling(t *testing.T) {
	dir := t.TempDir()
	bin, pkgs := filepath.Join(dir, "bin"), filepath.Join(dir, "pkgs")
	for _, path := range []string{bin, pkgs} {
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(dir, "installed")
	t.Setenv("KNO_TEST_INSTALL", marker)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for name, script := range map[string]string{
		"id":   "echo 1000",
		"su":   "[ \"$1\" = root ] && [ \"$2\" = -c ] || exit 81; exec sh -c \"$3\"",
		"sudo": "exit 82",
		"kno":  "if [ \"$2\" = --help ]; then echo --kno; else printf '%s\\n' \"$@\" >\"$KNO_TEST_INSTALL\"; fi",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	names := []string{"kno_1_amd64.deb", "kno-host-config-windfall1_1_amd64.deb"}
	var sums strings.Builder
	for _, name := range names {
		data := []byte("package " + name)
		if err := os.WriteFile(filepath.Join(pkgs, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256(data), name)
	}
	if err := os.WriteFile(filepath.Join(pkgs, build.SumsName), []byte(sums.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	command := bootstrapCmdWith(dir, names, "su")
	if out, err := exec.Command("sh", "-ec", command).CombinedOutput(); err != nil {
		t.Fatalf("su bootstrap: %s (%v)", out, err)
	}
	if data, err := os.ReadFile(marker); err != nil || !strings.Contains(string(data), "activate\n--kno\n") {
		t.Fatalf("bootstrap did not install through kno: %q (%v)", data, err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgs, names[0]), []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("sh", "-ec", command).CombinedOutput(); err == nil {
		t.Fatalf("corrupt package installed: %s", out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("installer ran after digest failure")
	}
}
