package quarantine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/track"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestClearingOneTagKeepsTheRest(t *testing.T) {
	p := filepath.Join(t.TempDir(), "quarantine")
	write(t, p, "v0.0.102\nv0.0.104\n")

	ok, err := Clear(p, "v0.0.104")
	if err != nil || !ok {
		t.Fatalf("Clear = %v %v", ok, err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) != "v0.0.102" {
		t.Errorf("file is now %q", string(b))
	}
}

func TestAnEmptiedListIsRemoved(t *testing.T) {
	p := filepath.Join(t.TempDir(), "quarantine")
	write(t, p, "v0.0.104\n")
	if ok, err := Clear(p, "v0.0.104"); err != nil || !ok {
		t.Fatalf("Clear = %v %v", ok, err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("an empty quarantine file was left behind: %v", err)
	}
}

func TestClearingATagThatIsNotThereReportsSo(t *testing.T) {
	p := filepath.Join(t.TempDir(), "quarantine")
	write(t, p, "v0.0.102\n")
	if ok, _ := Clear(p, "v0.0.104"); ok {
		t.Error("a tag that was not quarantined was reported as cleared")
	}
	if ok, _ := Clear(filepath.Join(t.TempDir(), "nope"), "v0.0.104"); ok {
		t.Error("a missing quarantine file was reported as cleared")
	}
}

func TestTheReportNamesTheHostAndThePath(t *testing.T) {
	state := t.TempDir()
	write(t, filepath.Join(knopath.HostState(state, "mixy"), "quarantine"), "v0.0.104\n")
	e := track.Env{
		StateDir: state,
		Config: config.Config{Hosts: map[string]config.Host{
			"mixy": {SSH: "root@mixy"},
			"reed": {SSH: "root@reed"},
		}},
	}
	files := Files(e)
	if len(files) != 1 || files[0].Host != "mixy" {
		t.Fatalf("Files = %+v, want mixy only", files)
	}

	var b bytes.Buffer
	Report(&b, files)
	for _, want := range []string{"mixy", "v0.0.104", "kno unquarantine"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("the report does not mention %q:\n%s", want, b.String())
		}
	}

	if !strings.Contains(b.String(), "repair what broke it first") {
		t.Errorf("the report does not say to repair first:\n%s", b.String())
	}
}

func TestClearingOneTagEverywhereLeavesTheOthers(t *testing.T) {
	state := t.TempDir()
	for host, tags := range map[string]string{
		"mixy":  "v0.0.104\n",
		"reed":  "v0.0.102\nv0.0.104\n",
		"lilly": "v0.0.102\n",
	} {
		write(t, filepath.Join(knopath.HostState(state, host), "quarantine"), tags)
	}
	e := track.Env{StateDir: state, Config: config.Config{Hosts: map[string]config.Host{
		"mixy": {SSH: "root@mixy"}, "reed": {SSH: "root@reed"},
		"lilly": {SSH: "root@lilly"}, "haywood": {SSH: "root@haywood"},
	}}}

	cleared := 0
	for _, f := range Files(e) {
		ok, err := Clear(f.Path, "v0.0.104")
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			cleared++
		}
	}
	if cleared != 2 {
		t.Errorf("cleared %d host(s), want the 2 holding v0.0.104", cleared)
	}

	if _, err := os.Stat(filepath.Join(knopath.HostState(state, "mixy"), "quarantine")); !os.IsNotExist(err) {
		t.Errorf("mixy's emptied list was left behind: %v", err)
	}

	for _, host := range []string{"reed", "lilly"} {
		b, err := os.ReadFile(filepath.Join(knopath.HostState(state, host), "quarantine"))
		if err != nil {
			t.Fatalf("%s lost its other quarantine: %v", host, err)
		}
		if strings.TrimSpace(string(b)) != "v0.0.102" {
			t.Errorf("%s now holds %q, want v0.0.102", host, string(b))
		}
	}

	if got := Files(e); len(got) != 2 {
		t.Errorf("Files = %d, want the 2 hosts that still hold a tag: %+v", len(got), got)
	}
}

func TestAnOperatorIsSentToTheUpdatersList(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, "state", "quarantine"), "v0.0.104\n")
	old := updaterHome
	updaterHome = home
	t.Cleanup(func() { updaterHome = old })
	t.Setenv("KNO_USER", "")

	mine := t.TempDir()
	e := track.Env{StateDir: mine, QuarantineFile: filepath.Join(mine, "quarantine")}
	var b bytes.Buffer
	if code := run(&b, e, "v0.0.104", "", false); code != 1 {
		t.Fatalf("exit = %d:\n%s", code, b.String())
	}
	if want := "sudo -u kno KNO_HOME=" + home + " kno unquarantine v0.0.104"; !strings.Contains(b.String(), want) {
		t.Errorf("the operator is not told %q:\n%s", want, b.String())
	}

	b.Reset()
	e = track.Env{StateDir: filepath.Join(home, "state"), QuarantineFile: filepath.Join(home, "state", "quarantine")}
	if code := run(&b, e, "v0.0.104", "", false); code != 0 || strings.Contains(b.String(), "sudo -u") {
		t.Errorf("exit = %d: the updater itself got the hint:\n%s", code, b.String())
	}
}
