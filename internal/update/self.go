package update

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/katzenpost/kno/internal/build"
	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/pkg/freebsd"
	"github.com/katzenpost/kno/internal/track"
)

type selfUpgrade struct {
	Env  track.Env
	Log  track.Logger
	Sudo string

	installed string
	running   func() string
	have      string
	want      func(tag string) string
	build     func(tag, rel string) (string, error)
	run       func(argv []string) error
	version   func(bin string) string
}

func newSelfUpgrade(e track.Env, l track.Logger, sudo string) selfUpgrade {
	installed := knopath.InstalledBin
	if runtime.GOOS == "freebsd" {
		installed = knopath.LocalBin
	}
	return selfUpgrade{
		Env: e, Log: l, Sudo: sudo,
		installed: installed,
		running: func() string {
			exe, err := os.Executable()
			if err != nil {
				return ""
			}
			return fsutil.Resolve(exe)
		},
		have: cli.Version(),
		want: func(tag string) string { return build.ToolVersion(e.Repo, e.Network, tag) },
		build: func(tag, rel string) (string, error) {
			return build.KnoPackage(e.Repo, e.Network, tag, rel)
		},
		run: func(argv []string) error {
			cmd := exec.Command(argv[0], argv[1:]...)
			cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
			cmd.Stdin = os.Stdin
			return cmd.Run()
		},
		version: func(bin string) string {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bin, "--version")
			cmd.WaitDelay = time.Second
			out, _ := cmd.Output()
			return strings.TrimSpace(string(out))
		},
	}
}

func (s selfUpgrade) installArgv(pkg string) []string {
	switch {
	case s.Sudo != "":
		return cli.Elevate(s.Sudo, s.installed, "activate", "--kno", pkg)
	case strings.HasSuffix(pkg, ".pkg"):
		return freebsd.PkgAdd("", pkg)
	}
	return []string{"env", "DEBIAN_FRONTEND=noninteractive", "dpkg", "-i", pkg}
}

func (s selfUpgrade) Run() error {
	if os.Getenv("KNO_SELF_UPGRADE") == "0" || s.running() != s.installed {
		return nil
	}
	if st := readLine(filepath.Join(s.Env.StateDir, "state")); st != "" && st != "steady" {
		return nil
	}
	tag := s.Env.CurrentVersion()
	if tag == "" {
		return nil
	}
	want := s.want(tag)
	if want == "" || want == s.have {
		return nil
	}
	if deb.CompareVersions(want, s.have) <= 0 {
		s.Log.Logf("kno %s runs here, and this repository builds the older %s. kno keeps it", s.have, want)
		return nil
	}
	s.Log.Logf("upgrading kno %s -> %s", s.have, want)
	pkg, err := s.build(tag, filepath.Join(s.Env.ReleasesDir, tag))
	if err != nil {
		return fmt.Errorf("build kno %s: %w", want, err)
	}
	keep := filepath.Join(s.Env.StateDir, "kno-installed"+filepath.Ext(pkg))
	kept := ""
	if _, err := os.Stat(keep); err == nil {
		kept = keep
	}
	if err := s.run(s.installArgv(pkg)); err != nil {
		return fmt.Errorf("install kno %s: %w", want, err)
	}
	if got := s.version(s.installed); !versionIs(got, want) {
		if kept != "" {
			s.Log.Errorf("the new kno says %q, not %s. kno installs again the kno that this host had", got, want)
			if err := s.run(s.installArgv(kept)); err != nil {
				return fmt.Errorf("the new kno does not run, and reinstalling %s failed too: %w", kept, err)
			}
			return fmt.Errorf("kno %s did not run here. This host keeps %s", want, s.have)
		}
		return fmt.Errorf("the new kno says %q, not %s, and no earlier kno package is kept here", got, want)
	}
	_ = os.Remove(keep)
	if err := fsutil.CopyFile(pkg, keep); err != nil {
		return err
	}
	s.Log.Logf("kno is now %s", want)
	return nil
}

func versionIs(output, want string) bool { return strings.TrimSpace(output) == "kno "+want }
