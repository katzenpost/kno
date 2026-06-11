package source

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func taggedRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q", "-b", "main")
	for _, tag := range []string{"v0.0.9", "v0.0.10", "v0.0.2"} {
		if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(tag+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git(t, dir, "add", "-A")
		git(t, dir, "commit", "-qm", tag)
		git(t, dir, "tag", tag)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("untagged tip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "tip")
	return dir
}

func writePins(t *testing.T, dir, src string) string {
	t.Helper()
	path := filepath.Join(dir, "pins.env")
	body := strings.Join([]string{
		"GO_VERSION=1.26.2",
		"",
		"KATZENPOST_URL=file://" + src,
		"KATZENPOST_REF=v0.0.2",
		"KATZENPOST_PIN_MODE=tag",
		"KATZENPOST_SRC=" + src,
		"",
		"HPQC_REF=v0.0.86",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRepinTakesTheNewestVersionNotTheNewestString(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	tmp := t.TempDir()
	src := taggedRepo(t, filepath.Join(tmp, "kp.git"))
	path := writePins(t, tmp, src)

	ref, err := Repin(path, PinTag)
	if err != nil {
		t.Fatalf("Repin: %v", err)
	}
	if ref != "v0.0.10" {
		t.Errorf("pinned %s, want v0.0.10", ref)
	}
	pins, err := ParsePins(path)
	if err != nil {
		t.Fatal(err)
	}
	if pins["KATZENPOST_REF"] != "v0.0.10" {
		t.Errorf("KATZENPOST_REF = %s", pins["KATZENPOST_REF"])
	}

	if pins["HPQC_REF"] != "v0.0.86" || pins["KATZENPOST_SRC"] != src {
		t.Errorf("re-pin disturbed another line: %v", pins)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), "GO_VERSION=1.26.2\n\n") {
		t.Errorf("re-pin rewrote the file's shape:\n%s", body)
	}
}

func TestRepinRecordsTheModeItUsed(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	tmp := t.TempDir()
	src := taggedRepo(t, filepath.Join(tmp, "kp.git"))
	path := writePins(t, tmp, src)

	ref, err := Repin(path, PinTip)
	if err != nil {
		t.Fatalf("Repin: %v", err)
	}
	head, err := exec.Command("git", "-C", src, "rev-parse", "main").Output()
	if err != nil {
		t.Fatal(err)
	}
	if ref != strings.TrimSpace(string(head)) {
		t.Errorf("tip pinned %s, want %s", ref, strings.TrimSpace(string(head)))
	}
	pins, err := ParsePins(path)
	if err != nil {
		t.Fatal(err)
	}
	if pins["KATZENPOST_PIN_MODE"] != PinTip {
		t.Errorf("KATZENPOST_PIN_MODE = %q, want %q", pins["KATZENPOST_PIN_MODE"], PinTip)
	}

	if len(ref) != 40 {
		t.Errorf("tip pinned %q, which is not a commit", ref)
	}
}

func TestRepinResolvesFromTheRemoteWhenThereIsNoLocalClone(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	tmp := t.TempDir()
	src := taggedRepo(t, filepath.Join(tmp, "kp.git"))
	ref, err := ResolveRef(PinTag, "file://"+src, "")
	if err != nil {
		t.Fatalf("ResolveRef: %v", err)
	}
	if ref != "v0.0.10" {
		t.Errorf("remote tag resolve gave %s, want v0.0.10", ref)
	}
}

func TestRepinRejectsAnUnknownMode(t *testing.T) {
	if _, err := ResolveRef("whenever", "file:///nowhere", ""); err == nil {
		t.Fatal("an unknown pin mode was accepted")
	}
}

func TestRepinRefusesAPinsFileWithNothingToPin(t *testing.T) {
	if _, err := setPins("GO_VERSION=1.26.2\n", map[string]string{"KATZENPOST_REF": "v1"}); err == nil {
		t.Fatal("setPins accepted a file with no KATZENPOST_REF")
	}
}

func TestSetPinsAddsAMissingModeNextToTheRef(t *testing.T) {
	in := "KATZENPOST_URL=u\nKATZENPOST_REF=v1\nHPQC_REF=v2\n"
	out, err := setPins(in, map[string]string{"KATZENPOST_REF": "v9", "KATZENPOST_PIN_MODE": "tip"})
	if err != nil {
		t.Fatal(err)
	}
	want := "KATZENPOST_URL=u\nKATZENPOST_REF=v9\nKATZENPOST_PIN_MODE=tip\nHPQC_REF=v2\n"
	if out != want {
		t.Errorf("got:\n%q\nwant:\n%q", out, want)
	}
}
