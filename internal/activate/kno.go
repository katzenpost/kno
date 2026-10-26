package activate

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/pkg/freebsd"
	"github.com/katzenpost/kno/internal/track"
)

func knoPackage(path string) bool {
	if strings.HasSuffix(path, ".pkg") {
		stem, ok := freebsd.SplitPkgName(filepath.Base(path))
		return ok && (stem == "kno" || strings.HasPrefix(stem, "kno-host-config"))
	}
	control, _, err := deb.Control(path)
	if err != nil {
		return false
	}
	name := deb.Field(control, "Package")
	return name == "kno" || strings.HasPrefix(name, "kno-host-config")
}

func knoInstallPlan(debs []string, required bool) (Plan, error) {
	for _, d := range debs {
		if !knoPackage(d) {
			return Plan{}, fmt.Errorf("%s is not a kno or kno-host-config package", d)
		}
	}
	if required {
		return Plan{}, fmt.Errorf("the record has no digest for kno packages, and require_signatures" +
			" is on. Install them with: make install")
	}
	needs, argv := "dpkg", append([]string{"env", "DEBIAN_FRONTEND=noninteractive", "dpkg", "-i"}, debs...)
	if strings.HasSuffix(debs[0], ".pkg") {
		needs, argv = "pkg", freebsd.PkgAdd("", debs...)
	}
	for _, d := range debs[1:] {
		if strings.HasSuffix(d, ".pkg") != strings.HasSuffix(debs[0], ".pkg") {
			return Plan{}, fmt.Errorf("%s and %s are not the same package format", debs[0], d)
		}
	}
	return Plan{Backend: "kno", Needs: needs, Packages: debs, Steps: []Step{{Kind: StepRun,
		Argv: argv, Log: "install " + strings.Join(debs, " ")}}}, nil
}

func runKno(stderr io.Writer, debs []string, dry bool) int {
	l := track.Logger{Tool: "activate", W: stderr}
	if len(debs) == 0 {
		fmt.Fprintln(stderr, "usage: kno activate --kno <deb>...")
		return 1
	}
	dir, err := os.MkdirTemp("", "kno-activate-")
	if err != nil {
		l.Errorf("%v", err)
		return 1
	}
	defer os.RemoveAll(dir)
	copies := make([]string, len(debs))
	for i, d := range debs {
		if fi, err := os.Lstat(d); err != nil || !fi.Mode().IsRegular() {
			l.Errorf("%s is not a regular file. kno installs nothing", d)
			return 1
		}
		copies[i] = filepath.Join(dir, filepath.Base(d))
		if err := fsutil.CopyFile(d, copies[i]); err != nil {
			l.Errorf("%v", err)
			return 1
		}
	}
	p, err := knoInstallPlan(copies, signaturesRequired())
	if err != nil {
		l.Errorf("%v", err)
		return 1
	}
	if err := Apply(context.Background(), p, Options{DryRun: dry}, stderr); err != nil {
		l.Errorf("%v", err)
		return 1
	}
	return 0
}

func restartSteps(units []string) []Step {
	var out []Step
	for _, u := range units {
		if !unitName.MatchString(u) {
			continue
		}
		out = append(out,
			Step{Kind: StepRunIgnore, Argv: []string{"systemctl", "reset-failed", "--", u}},
			Step{Kind: StepRunIgnore, Argv: []string{"systemctl", "restart", "--", u},
				Log: "restart " + u, Warn: true})
	}
	return out
}
