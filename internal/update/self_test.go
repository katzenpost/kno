package update

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/track"
)

type selfFake struct {
	ran      [][]string
	versions []string
	built    int
}

func newSelfFake(t *testing.T, have, want string, f *selfFake) selfUpgrade {
	e := newEnv(t)
	write(t, e.CurrentFile, "v0.0.107\n")
	write(t, filepath.Join(e.StateDir, "state"), "steady\n")
	pkg := filepath.Join(t.TempDir(), "kno-new.pkg")
	write(t, pkg, "new\n")
	return selfUpgrade{
		Env: e, Log: track.Logger{Tool: "update", W: io.Discard},
		installed: "/usr/local/bin/kno",
		running:   func() string { return "/usr/local/bin/kno" },
		have:      have,
		want:      func(string) string { return want },
		build:     func(string, string) (string, error) { f.built++; return pkg, nil },
		run:       func(argv []string) error { f.ran = append(f.ran, argv); return nil },
		version: func(string) string {
			v := f.versions[0]
			f.versions = f.versions[1:]
			return v
		},
	}
}

func TestANewerKnoIsBuiltInstalledAndChecked(t *testing.T) {
	f := &selfFake{versions: []string{"kno 0.0.107+2"}}
	s := newSelfFake(t, "0.0.107+1", "0.0.107+2", f)
	if err := s.Run(); err != nil {
		t.Fatal(err)
	}
	if f.built != 1 || len(f.ran) != 1 || !strings.Contains(strings.Join(f.ran[0], " "), "pkg add -f") {
		t.Fatalf("built %d, ran %v", f.built, f.ran)
	}
	if _, err := os.Stat(filepath.Join(s.Env.StateDir, "kno-installed.pkg")); err != nil {
		t.Errorf("the installed kno package is not kept for the next rollback: %v", err)
	}
}

func TestTheSameKnoIsLeftAlone(t *testing.T) {
	f := &selfFake{}
	s := newSelfFake(t, "0.0.107+2", "0.0.107+2", f)
	if err := s.Run(); err != nil || f.built != 0 || len(f.ran) != 0 {
		t.Fatalf("err %v, built %d, ran %v", err, f.built, f.ran)
	}
}

func TestKnoIsNeverDowngraded(t *testing.T) {
	f := &selfFake{}
	s := newSelfFake(t, "0.0.107+3", "0.0.107+2", f)
	if err := s.Run(); err != nil || f.built != 0 {
		t.Fatalf("err %v, built %d", err, f.built)
	}
}

func TestACheckoutBuildDoesNotUpgradeItself(t *testing.T) {
	f := &selfFake{}
	s := newSelfFake(t, "0.0.107+1", "0.0.107+2", f)
	s.running = func() string { return "/home/op/kno/bin/kno" }
	if err := s.Run(); err != nil || f.built != 0 {
		t.Fatalf("a kno run from a checkout replaced the installed one: err %v, built %d", err, f.built)
	}
}

func TestNoUpgradeWhileAReleaseIsPending(t *testing.T) {
	f := &selfFake{}
	s := newSelfFake(t, "0.0.107+1", "0.0.107+2", f)
	write(t, filepath.Join(s.Env.StateDir, "state"), "pending\n")
	if err := s.Run(); err != nil || f.built != 0 {
		t.Fatalf("kno upgraded itself during a verify window: err %v, built %d", err, f.built)
	}
}

func TestAKnoThatDoesNotRunIsReplacedByTheKeptOne(t *testing.T) {
	f := &selfFake{versions: []string{"exec format error"}}
	s := newSelfFake(t, "0.0.107+1", "0.0.107+2", f)
	write(t, filepath.Join(s.Env.StateDir, "kno-installed.pkg"), "old\n")
	err := s.Run()
	if err == nil || !strings.Contains(err.Error(), "keeps 0.0.107+1") {
		t.Fatalf("err = %v", err)
	}
	if len(f.ran) != 2 || !strings.HasSuffix(f.ran[1][len(f.ran[1])-1], "kno-installed.pkg") {
		t.Errorf("the kept kno was not reinstalled: %v", f.ran)
	}
}

func TestDebianInstallUsesSudoForAUser(t *testing.T) {
	s := selfUpgrade{Sudo: "sudo", installed: "/usr/bin/kno"}
	if got := strings.Join(s.installArgv("/r/kno_1_amd64.deb"), " "); got != "sudo /usr/bin/kno activate --kno /r/kno_1_amd64.deb" {
		t.Errorf("argv = %q", got)
	}
	s.Sudo = ""
	if got := strings.Join(s.installArgv("/r/kno_1_amd64.deb"), " "); got != "env DEBIAN_FRONTEND=noninteractive dpkg -i /r/kno_1_amd64.deb" {
		t.Errorf("argv as root = %q", got)
	}
}

func TestFreeBSDInstallUsesSudoForAUser(t *testing.T) {
	s := selfUpgrade{Sudo: "sudo", installed: "/usr/local/bin/kno"}
	if got := strings.Join(s.installArgv("/r/kno-1.pkg"), " "); got != "sudo /usr/local/bin/kno activate --kno /r/kno-1.pkg" {
		t.Errorf("argv = %q", got)
	}
	s.Sudo = ""
	if got := strings.Join(s.installArgv("/r/kno-1.pkg"), " "); got != "env ASSUME_ALWAYS_YES=yes pkg add -f /r/kno-1.pkg" {
		t.Errorf("argv as root = %q", got)
	}
}
