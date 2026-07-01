package freebsd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBinaryNamesMatchTheDebianPackage(t *testing.T) {
	for _, c := range upstreamCmds {
		want := c
		if !strings.HasPrefix(want, "kp") {
			want = "kp" + want
		}
		if got := filepath.Base(InstallPath(c)); got != want {
			t.Errorf("cmd/%s installs as %s on FreeBSD; katzenpost's Debian package names it %s", c, got, want)
		}
	}
}

func TestDaemonsGoToSbinAndToolsToBin(t *testing.T) {
	for c, want := range map[string]string{
		"server":    "usr/local/sbin/kpserver",
		"dirauth":   "usr/local/sbin/kpdirauth",
		"replica":   "usr/local/sbin/kpreplica",
		"courier":   "usr/local/sbin/kpcourier",
		"kpclientd": "usr/local/bin/kpclientd",
		"fetch":     "usr/local/bin/kpfetch",
	} {
		if got := InstallPath(c); got != want {
			t.Errorf("InstallPath(%s) = %s, want %s", c, got, want)
		}
	}
}

func TestCommandsAreEveryMainPackageUnderCmd(t *testing.T) {
	src := t.TempDir()
	for dir, body := range map[string]string{
		"cmd/server/main.go":        "package main\n",
		"cmd/kpconfig/main.go":      "// x\npackage main\n\nfunc main() {}\n",
		"cmd/internal/lib.go":       "package internal\n",
		"cmd/onlytest/main_test.go": "package main\n",
	} {
		p := filepath.Join(src, dir)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Commands(src)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "kpconfig server" {
		t.Errorf("Commands = %v, want [kpconfig server]", got)
	}
}

func TestLdflagsStampTheUpstreamVersion(t *testing.T) {
	got := Ldflags("v0.0.107")
	for _, want := range []string{"-s -w", "-X github.com/katzenpost/katzenpost/common.BuildVersion=0.0.107"} {
		if !strings.Contains(got, want) {
			t.Errorf("Ldflags = %q lacks %q, which katzenpost's debian/rules passes", got, want)
		}
	}
	for _, want := range []string{"-buildid=", "-linkmode=internal"} {
		if !strings.Contains(got, want) {
			t.Errorf("Ldflags = %q lacks %q: without it a native FreeBSD build and the cross build differ", got, want)
		}
	}
}

func TestFreeBSDBuildsLikeTheDebianRules(t *testing.T) {
	got := strings.Join(goBuild("v0.0.107", "/out/server"), " ")
	for _, want := range []string{"-trimpath", "-buildvcs=false", Ldflags("v0.0.107")} {
		if !strings.Contains(got, want) {
			t.Errorf("the FreeBSD build %q lacks %q, which katzenpost's debian/rules passes", got, want)
		}
	}
}
