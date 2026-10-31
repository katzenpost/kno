package update

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"

	"github.com/katzenpost/kno/internal/build"
	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/source"
	"github.com/katzenpost/kno/internal/track"
)

var installedToolVersion = regexp.MustCompile(`^kno ([0-9][0-9A-Za-z.+:~-]*)$`)

func updateHelper(e track.Env) string {
	if label, _, ok := config.LocalHost(e.Config); ok {
		return cli.RootHelper(e.Config.Privilege(label))
	}
	return cli.Sudo()
}

func updateKno(out io.Writer, v Flags) int {
	l := track.Logger{Tool: "update", W: out}
	e, err := track.LoadEnv()
	if err != nil {
		l.Errorf("cannot read kno.toml: %v", err)
		return 1
	}
	switch policyOf(e, v) {
	case "off":
		l.Logf("update policy is 'off'. kno applies no update")
		return 0
	case "simulate":
		v.Dry = true
	case "apply", "":
	default:
		l.Errorf("unknown update policy %q", v.Policy)
		return 1
	}
	if v.Dry {
		selected := v.Tag
		if selected == "" {
			selected = v.Ref
		}
		if selected == "" {
			selected = "main"
		}
		l.Logf("DRYRUN: fetch kno %s into %s", selected, e.KnoSourceDir())
		l.Logf("DRYRUN: build kno and this host's matching configuration package. Install both and verify kno --version")
		l.Logf("DRYRUN: latest source was not fetched and no package was built or installed")
		return 0
	}
	if err := runKnoUpdate(e, l, v.Tag, v.Ref, v.AllowDowngrade); err != nil {
		l.Errorf("%v", err)
		return 1
	}
	return 0
}

func runKnoUpdate(e track.Env, l track.Logger, tag, ref string, allowDowngrade bool) error {
	label, _, local := config.LocalHost(e.Config)
	if !local {
		return fmt.Errorf("kno update kno needs a configured local host. kno cannot choose a matching host package")
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "freebsd" {
		return fmt.Errorf("kno update kno needs a Debian/Ubuntu host or FreeBSD pkg")
	}
	if runtime.GOOS == "freebsd" && runtime.GOARCH != "amd64" {
		return fmt.Errorf("the pinned FreeBSD ABI supports amd64. This host is %s", runtime.GOARCH)
	}
	required := e.RequireSignatures
	if c, err := config.Load(config.SystemPath()); err == nil {
		required = required || c.Update.RequireSignatures
	}
	if required {
		return fmt.Errorf("require_signatures is on. kno has no signed digest record for its own package. kno installs nothing")
	}
	if tag == "" && ref == "" {
		ref = "main"
	}
	if ref != "" && e.VerifyTag {
		return fmt.Errorf("require_signed_tag is on. A selected commit is not a signed kno release tag")
	}
	if err := os.MkdirAll(e.StateDir, 0o755); err != nil {
		return err
	}
	unlock, locked, err := fsutil.TryLock(filepath.Join(e.StateDir, "update.lock"))
	if err != nil {
		return err
	}
	if !locked {
		return fmt.Errorf("another kno update is active. Wait for it to finish")
	}
	defer unlock()
	unlockBuild, err := fsutil.Lock(filepath.Join(e.StateDir, "build-kno.lock"), nil)
	if err != nil {
		return err
	}
	defer unlockBuild()
	if state := readLine(filepath.Join(e.StateDir, "state")); state != "" && state != "steady" {
		return fmt.Errorf("a Katzenpost deployment is %s. Finish its verification before you replace kno", state)
	}
	s := newSelfUpgrade(e, l, updateHelper(e))
	m := installedToolVersion.FindStringSubmatch(s.version(s.installed))
	if len(m) != 2 {
		return fmt.Errorf("cannot identify the installed kno at %s. kno installs nothing", s.installed)
	}
	dataRepo := e.Repo
	selected, err := e.EnsureKnoSource(l, tag, ref)
	if err != nil {
		return err
	}
	pins, err := source.ParsePins(filepath.Join(e.Repo, "thirdparty", "pins.env"))
	if err != nil {
		return err
	}
	packageTag := pins["KATZENPOST_REF"]
	if !track.SafeRef(packageTag) {
		return fmt.Errorf("selected kno source has no valid pinned package version")
	}
	want := build.SelectedToolVersion(e.Repo, e.Network, packageTag, selected)
	if err := knoDowngrade(m[1], want, allowDowngrade); err != nil {
		return err
	}
	pkgs, err := build.KnoPackagesForSelection(e, selected, dataRepo, label, true, l.W)
	if err != nil {
		return err
	}
	return installKnoGroup(s, pkgs, want)
}

func knoDowngrade(have, want string, allowed bool) error {
	if deb.CompareVersions(want, have) < 0 && !allowed {
		return fmt.Errorf("installed kno %s is newer than selected %s. Use --allow-downgrade to allow the downgrade", have, want)
	}
	return nil
}

func installKnoGroup(s selfUpgrade, pkgs []string, want string) error {
	if len(pkgs) != 2 {
		return fmt.Errorf("selected kno needs its tool and matching host configuration package. kno installs nothing")
	}
	for _, pkg := range pkgs {
		fi, err := os.Lstat(pkg)
		if err != nil || !fi.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular package file. kno installs nothing", pkg)
		}
	}
	argv := append([]string{"env", "KNO_SKIP_BOOTSTRAP=1", s.installed, "activate", "--kno"}, pkgs...)
	if err := s.run(cli.Elevate(s.Sudo, argv...)); err != nil {
		return fmt.Errorf("install kno package group %s: %w. Verify both package states before retrying", want, err)
	}
	if got := s.version(s.installed); !versionIs(got, want) {
		return fmt.Errorf("the installed kno says %q, expected %s. Verify both package states before retrying", got, want)
	}
	s.Log.Logf("kno and its host configuration package are now %s", want)
	return nil
}
