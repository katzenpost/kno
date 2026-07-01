package deb

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/parallel"

	"github.com/katzenpost/kno/internal/knopath"
)

const DefaultEpoch = "1600000000"

var debImageDigests = map[string]string{
	"amd64":   "sha256:b29f74a267526ae6ea104eed6c46133b0ca70ce812525df8cd5817698f0a624a",
	"arm64":   "sha256:6bd27d44e6c32a66bbd72d7cb2b76a8ae3497ec2e5274a81abd1b37f6013fa1f",
	"arm":     "sha256:09a5970b082d5d26587f315dd4751b0b534c2efe75f1fb6d55d84d790180efa5",
	"386":     "sha256:829d25d5de8c8da7f7f9e343d09f11cb20ba4266460f9f7c92cd9f2ecb45a170",
	"ppc64le": "sha256:1a51c4ab0470eee849b8a2b565729259eb64159d80dd6481330a17853dcc969f",
}

func debImageDigest() (string, error) {
	d, ok := debImageDigests[runtime.GOARCH]
	if !ok {
		return "", fmt.Errorf("no pinned dpkg-deb image for %s; packaging needs one"+
			" (set DEB_IMAGE to an image with dpkg-deb, or add the digest to debImageDigests)",
			runtime.GOARCH)
	}
	return d, nil
}

func SourceDateEpoch(repo, tag string) string {
	return SourceDateEpochWith(EnvSettings(), repo, tag)
}

func Epoch(repo, tag string) (int64, error) {
	sde := SourceDateEpoch(repo, tag)
	epoch, err := strconv.ParseInt(sde, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bad SOURCE_DATE_EPOCH %q: %w", sde, err)
	}
	return epoch, nil
}

func SourceDateEpochWith(st Settings, repo, tag string) string {
	if v := st.SourceDateEpoch; v != "" {
		return v
	}
	out, err := exec.Command("git", "-C", repo, "log", "-1", "--format=%ct",
		"refs/tags/"+tag).Output()
	if s := strings.TrimSpace(string(out)); err == nil && s != "" {
		return s
	}

	out, err = exec.Command("git", "-C", repo, "log", "-1", "--format=%ct",
		"--", "thirdparty/pins.env").Output()
	if s := strings.TrimSpace(string(out)); err == nil && s != "" {
		return s
	}
	return DefaultEpoch
}

func normalizeTimes(root, epoch string) error {
	sec, err := strconv.ParseInt(epoch, 10, 64)
	if err != nil {
		return fmt.Errorf("bad SOURCE_DATE_EPOCH %q: %w", epoch, err)
	}
	t := time.Unix(sec, 0)
	return filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		return os.Chtimes(p, t, t)
	})
}

func normalizeModes(root string) error {
	return filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			return nil
		case fi.IsDir():
			return os.Chmod(p, 0o755)
		}
		return os.Chmod(p, fsutil.PackMode(fi.Mode()))
	})
}

func prepareImage(ctx context.Context, st Settings) error {
	if st.BuildHook != "" {
		return nil
	}
	image, err := st.image()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, compile.Podman(), "pull", "-q", image)
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("pull %s: %w\n%s", image, err, b)
	}
	return nil
}

func Build(ctx context.Context, root, out string) error {
	return BuildWith(ctx, EnvSettings(), root, out)
}

func BuildWith(ctx context.Context, st Settings, root, out string) error {
	sde := st.epoch()
	if err := normalizeModes(root); err != nil {
		return err
	}
	if err := normalizeTimes(root, sde); err != nil {
		return err
	}
	if db := st.BuildHook; db != "" {
		cmd := exec.CommandContext(ctx, "sh", db, root, out)
		cmd.Env = append(os.Environ(), "SOURCE_DATE_EPOCH="+sde)
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	image, err := st.image()
	if err != nil {
		return err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	outAbs, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	args, done := compile.Contained([]string{"run", "--rm",
		"-e", "SOURCE_DATE_EPOCH=" + sde,
		"-v", compile.MustMount(rootAbs, "/pkgroot:ro"),
		"-v", compile.MustMount(filepath.Dir(outAbs), "/out:rw"),
		image, "dpkg-deb", "--root-owner-group", "--build", "/pkgroot",
		"/out/" + filepath.Base(outAbs)})
	defer done()
	cmd := exec.CommandContext(ctx, compile.Podman(), args...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

type stableVersion func(id PkgID, contentID, want string) string

type PkgID struct {
	Name string
	Arch string
	OS   string
}

func PackageConfigs(ctx context.Context, rel, tag, rev string,
	target func(svc string) (arch, osName string)) ([]string, error) {
	return PackageConfigsStable(ctx, rel, tag, rev, target, nil)
}

func PackageConfigsStable(ctx context.Context, rel, tag, rev string,
	target func(svc string) (arch, osName string), stable stableVersion) ([]string, error) {
	return PackageConfigsFlavoured(ctx, rel, tag, rev, rev, target, stable, nil)
}

func PackageConfigsFlavoured(ctx context.Context, rel, tag, rev, binRev string,
	target func(svc string) (arch, osName string), stable stableVersion,
	stamp func(svc string) string) ([]string, error) {
	frags, err := filepath.Glob(filepath.Join(rel, "manifest", "*.conf"))
	if err != nil {
		return nil, err
	}
	pkgs := filepath.Join(rel, "pkgs")
	if err := os.MkdirAll(pkgs, 0o755); err != nil {
		return nil, err
	}
	out := make([]string, len(frags))
	tasks := make([]parallel.Task, len(frags))
	for i, f := range frags {
		i, f := i, f
		tasks[i] = func(ctx context.Context) error {
			svc := strings.TrimSuffix(filepath.Base(f), ".conf")
			arch, osName := ConfigTarget(target, svc)
			name := knopath.ConfigPkgPrefix + svc
			st := ""
			if stamp != nil {
				st = stamp(svc)
			}
			parent := filepath.Join(rel, ".pkgbuild", "cfg-"+name)
			mk := func(r string) (string, error) {
				return AssembleConfig(ConfigRoot{
					Rel: rel, Tag: tag, Rev: r, BinRev: binRev, Svc: svc,
					RootParent: parent, Arch: arch, OS: osName, Stamp: st,
				})
			}
			root, err := mk(rev)
			if err != nil {
				return err
			}
			use := rev
			if stable != nil {

				if id, err := RootContentID(root); err == nil {

					want := configVersion(tag, rev, st)
					if v := stable(PkgID{Name: name, Arch: arch}, id, want); v != want {
						if r, s, ok := splitConfigVersion(tag, v); ok {
							use, st = r, s
							if err := os.RemoveAll(parent); err != nil {
								return err
							}
							root, err = mk(use)
							if err != nil {
								return err
							}
						}
					}
				}
			}
			deb := filepath.Join(pkgs, configDebName(name, tag, use, st, arch))
			if err := Build(ctx, root, deb); err != nil {
				return err
			}
			os.RemoveAll(parent)
			out[i] = deb
			return nil
		}
	}
	if err := prepareImage(ctx, EnvSettings()); err != nil {
		return nil, err
	}
	if err := parallel.Run(parallel.Jobs(), tasks); err != nil {
		return nil, err
	}
	os.RemoveAll(filepath.Join(rel, ".pkgbuild"))
	return out, nil
}

const ArchAll = "all"

func ConfigArch(archOf func(svc string) string, svc string) string {
	if archOf == nil {
		return ArchAll
	}
	if a := archOf(svc); a != "" {
		return a
	}
	return ArchAll
}

func ConfigTarget(target func(svc string) (string, string), svc string) (arch, osName string) {
	if target == nil {
		return ArchAll, ""
	}
	arch, osName = target(svc)
	if arch == "" {
		arch = ArchAll
	}
	return arch, osName
}
