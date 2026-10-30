package knodeb

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/knover"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/source"
)

type Options struct {
	Repo string

	Src    string
	Binary string

	Version string

	Arch string

	OS string

	OutDir string

	Maintainer string

	Image string

	Podman string

	Deb deb.Settings

	GoModCache, GoBuildCache string

	Verbose bool

	Out io.Writer
}

func (o Options) out() io.Writer {
	if o.Out != nil {
		return o.Out
	}
	return os.Stderr
}

const controlTemplate = `Package: kno
Version: %s
Architecture: %s
Maintainer: %s
Depends: ca-certificates
Recommends: podman, git
Section: admin
Priority: optional
Description: katzenpost network organizer (the kno tool)
 The kno binary: build, deploy, update and verify a katzenpost node. Every job
 is a subcommand (kno --help); the repository data it needs is compiled into
 the binary, so a checkout is optional.
`

const postinstScript = `#!/bin/sh
set -e
[ "$1" = configure ] || exit 0

if ! /usr/bin/kno bootstrap --package >&2; then
	echo "kno: host bootstrap did not complete; run 'sudo kno bootstrap --package' to retry" >&2
fi

`

func GoArch(arch string) string {
	switch arch {
	case "ppc64el":
		return "ppc64le"
	case "armhf":
		return "arm"
	case "i386":
		return "386"
	}
	return arch
}

func DefaultOptions(repo string) (Options, error) {
	binary, err := os.Executable()
	if err != nil {
		return Options{}, err
	}
	return defaultOptions(repo, cli.Version(), binary)
}

func defaultOptions(repo, runningVersion, binary string) (Options, error) {
	pins, err := source.ParsePins(filepath.Join(repo, "thirdparty", "pins.env"))
	if err != nil {
		return Options{}, err
	}
	img, err := source.PinnedImage(pins, "BUILD_IMAGE")
	if err != nil {
		return Options{}, err
	}

	src, sourceErr := sourceRoot(repo)
	version := knover.Version(repo, pins["KATZENPOST_REF"])
	if sourceErr != nil {
		version, err = installedPackageVersion(runningVersion)
		if err != nil {
			return Options{}, fmt.Errorf("%w; %v", sourceErr, err)
		}
	} else {
		binary = ""
	}
	gomod, gocache, _ := knopath.EnsureCaches()
	return Options{
		Repo:   repo,
		Src:    src,
		Binary: binary,

		Version:      version,
		Arch:         compile.HostDebArch(),
		OutDir:       filepath.Join(repo, "dist", "kno"),
		Maintainer:   deb.Maintainer(),
		Image:        img,
		Podman:       "podman",
		Deb:          deb.EnvSettings(),
		GoModCache:   gomod,
		GoBuildCache: gocache,
	}, nil
}

func (o Options) check() error {
	var missing []string
	for _, f := range [][2]string{
		{"Repo", o.Repo}, {"Version", o.Version}, {"Arch", o.Arch},
		{"OutDir", o.OutDir}, {"Maintainer", o.Maintainer}, {"Image", o.Image},
	} {
		if f[1] == "" {
			missing = append(missing, f[0])
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("knodeb: unset option(s): %s", strings.Join(missing, ", "))
	}
	return nil
}

func (o Options) source() string {
	if o.Src != "" {
		return o.Src
	}
	return o.Repo
}

func (o Options) podman() string {
	if o.Podman != "" {
		return o.Podman
	}
	return "podman"
}

func (o Options) goos() string {
	if o.OS != "" {
		return o.OS
	}
	return "linux"
}

func goBuild(out string) string {
	return "go build -trimpath -buildvcs=false" +
		` -ldflags "-X github.com/katzenpost/kno/internal/cli.stamped=$KNO_VERSION"` +
		" -o " + out + " ./cmd/kno"
}

func Compile(ctx context.Context, o Options, bindir string) error {
	return compileKno(ctx, o, bindir)
}

func CompileLocal(ctx context.Context, o Options, goVersion, out string) error {
	if o.Binary != "" {
		return o.copyInstalled(out)
	}
	src, err := sourceRoot(o.source())
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", goBuild(out))
	cmd.Dir = src
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+o.goos(), "GOARCH="+GoArch(o.Arch),
		"GOFLAGS=", "GOTOOLCHAIN="+compile.GoToolchain(goVersion), "KNO_VERSION="+o.Version)
	cmd.Stdout, cmd.Stderr = o.out(), o.out()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kno build (%s/%s, local go): %w", o.goos(), o.Arch, err)
	}
	return nil
}

func compileKno(ctx context.Context, o Options, bindir string) error {
	if o.Binary != "" {
		return o.copyInstalled(filepath.Join(bindir, "kno"))
	}
	src, err := sourceRoot(o.source())
	if err != nil {
		return err
	}

	args := []string{"run", "--rm",
		"--platform", "linux/" + runtime.GOARCH,
		"-v", compile.MustMount(src, "/repo:ro"),
		"-v", compile.MustMount(bindir, "/out:rw"),
		"-e", "CGO_ENABLED=0", "-e", "GOOS=" + o.goos(), "-e", "GOARCH=" + GoArch(o.Arch),
	}
	if o.GoModCache != "" {
		args = append(args, "-v", compile.MustMount(o.GoModCache, "/gomod:rw"), "-e", "GOMODCACHE=/gomod")
	}
	if o.GoBuildCache != "" {
		args = append(args, "-v", compile.MustMount(o.GoBuildCache, "/gocache:rw"), "-e", "GOCACHE=/gocache")
	}
	args = append(args, "-e", "KNO_VERSION="+o.Version)
	args = append(args, o.Image, "sh", "-c", "cd /repo && "+goBuild("/out/kno"))
	args, done := compile.Contained(args)
	defer done()
	cmd := exec.CommandContext(ctx, o.podman(), args...)
	var buf bytes.Buffer
	if o.Verbose {
		cmd.Stdout, cmd.Stderr = o.out(), o.out()
	} else {
		cmd.Stdout, cmd.Stderr = &buf, &buf
	}
	if err := cmd.Run(); err != nil {
		if !o.Verbose {
			fmt.Fprint(o.out(), buf.String())
		}
		return fmt.Errorf("kno build (%s/%s): %w", o.goos(), o.Arch, err)
	}
	return nil
}

func AssembleRoot(ctx context.Context, o Options) (string, error) {
	if err := o.check(); err != nil {
		return "", err
	}
	root := filepath.Join(o.OutDir, ".root")
	if err := os.RemoveAll(root); err != nil {
		return "", err
	}
	for _, d := range []string{"DEBIAN", "usr/bin"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			return "", err
		}
	}
	bindir := filepath.Join(root, "usr/bin")
	if err := compileKno(ctx, o, bindir); err != nil {
		return "", err
	}

	control := filepath.Join(root, "DEBIAN/control")
	body := fmt.Sprintf(controlTemplate, deb.Version(o.Version, ""), o.Arch, o.Maintainer)
	if err := os.WriteFile(control, []byte(body), 0o644); err != nil {
		return "", err
	}
	if err := os.Chmod(control, 0o644); err != nil {
		return "", err
	}
	post := filepath.Join(root, "DEBIAN/postinst")
	if err := os.WriteFile(post, []byte(postinstScript), 0o755); err != nil {
		return "", err
	}
	if err := os.Chmod(post, 0o755); err != nil {
		return "", err
	}
	if err := normalizeDirModes(root); err != nil {
		return "", err
	}
	return root, nil
}

func normalizeDirModes(root string) error {
	return filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil || !fi.IsDir() {
			return err
		}
		return os.Chmod(p, 0o755)
	})
}

func DebName(version, arch string) string {
	return "kno_" + deb.Version(version, "") + "_" + arch + ".deb"
}

func Package(ctx context.Context, o Options) (string, error) {
	root, err := AssembleRoot(ctx, o)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(o.OutDir, 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(o.OutDir, DebName(o.Version, o.Arch))
	if err := deb.BuildWith(ctx, o.Deb, root, out); err != nil {
		return "", err
	}
	return out, nil
}

func sourceRoot(repo string) (string, error) {
	for _, d := range []string{repo, filepath.Dir(repo)} {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d, nil
		}
	}
	return "", fmt.Errorf("no go.mod in %s or its parent: building kno needs a checkout of its source", repo)
}

var packageVersionPattern = regexp.MustCompile(`^[0-9][0-9A-Za-z.+:~-]*$`)

func installedPackageVersion(version string) (string, error) {
	version = strings.TrimPrefix(version, "v")
	if !packageVersionPattern.MatchString(version) {
		return "", fmt.Errorf("running kno version %q cannot identify an installable package", version)
	}
	return version, nil
}

func ToolVersion(repo, tag, revision string) string {
	if _, err := sourceRoot(repo); err != nil {
		if version, err := installedPackageVersion(cli.Version()); err == nil {
			return version
		}
	}
	return deb.ToolVersionRev(repo, tag, revision)
}

func (o Options) copyInstalled(out string) error {
	if o.goos() != runtime.GOOS || GoArch(o.Arch) != runtime.GOARCH {
		return fmt.Errorf("running kno is %s/%s, requested package is %s/%s; building kno for another platform requires a source checkout; run kno build from that checkout or set KNO_REPO to it", runtime.GOOS, runtime.GOARCH, o.goos(), o.Arch)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	if err := fsutil.CopyFile(o.Binary, out); err != nil {
		return err
	}
	return os.Chmod(out, 0o755)
}
