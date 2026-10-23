package sim

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/build"
	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/source"
)

func (r *run) pins() (map[string]string, error) {
	pins, err := source.ParsePins(filepath.Join(r.Repo, "thirdparty", "pins.env"))
	if err != nil {
		return nil, fmt.Errorf("reading pins: %w", err)
	}
	if pins["KATZENPOST_REF"] == "" {
		return nil, fmt.Errorf("no KATZENPOST_REF in pins.env")
	}
	return pins, nil
}

func (r *run) daemonsOf() ([]daemon, error) {
	svcs, err := ConfiguredServices(r.Repo, r.Network)
	if err != nil {
		return nil, err
	}
	out := make([]daemon, 0, len(svcs))
	for _, s := range svcs {
		node := nodeOf(s)
		out = append(out, daemon{Base: s,
			Arch: config.NodeArch(r.Repo, r.Network, node),
			OS:   r.containerOS(node)})
	}
	return out, nil
}

func (r *run) containerOS(node string) string {
	name := config.NodeOS(r.Repo, r.Network, node)
	if !strings.HasPrefix(name, "freebsd-") {
		return name
	}
	if r.log.W != nil {
		r.log.Logf("%s runs %s; the sim runs it in a Linux container, because it boots no FreeBSD guest yet", node, name)
	}
	return config.NodeOS(r.Repo, r.Network, "")
}

func (r *run) neededPlatforms(ds []daemon) []platform {
	seen := map[platform]bool{r.clientPlatform(): true}
	out := []platform{r.clientPlatform()}
	for _, d := range ds {
		p := r.platformOf(d)
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func debPattern(p platform) string {
	return "katzenpost_*+" + compile.OSToken(p.OS) + "_" + p.debArch() + ".deb"
}

func debPatterns(p platform) []string {
	return []string{debPattern(p), "katzenpost_[0-9]*-*_" + p.debArch() + ".deb"}
}

func pickDeb(candidates []string, p platform) string {
	for _, want := range debPatterns(p) {
		var hits []string
		for _, c := range candidates {
			if ok, _ := filepath.Match(want, filepath.Base(c)); ok {
				hits = append(hits, c)
			}
		}
		if best := newestDeb(hits); best != "" {
			return best
		}
	}
	return ""
}

func (r *run) provisionPackages() error {
	ds, err := r.daemonsOf()
	if err != nil {
		return err
	}
	pins, err := r.pins()
	if err != nil {
		return err
	}
	tag := pins["KATZENPOST_REF"]
	plats := r.neededPlatforms(ds)
	var have []string
	if r.Pkgs != "" {
		have, err = filepath.Glob(filepath.Join(r.Pkgs, "katzenpost_*.deb"))
		if err != nil {
			return err
		}
		r.log.Logf("using the katzenpost packages in %s", r.Pkgs)
	} else {
		have, err = r.buildRelease(tag, ds, plats)
		if err != nil {
			return err
		}
	}
	inPlace := r.Pkgs != "" && filepath.Clean(r.Pkgs) == filepath.Clean(r.pkgs)
	if !inPlace {
		if err := os.RemoveAll(r.pkgs); err != nil {
			return err
		}
		if err := os.MkdirAll(r.pkgs, 0o755); err != nil {
			return err
		}
	}
	for _, p := range plats {
		deb := pickDeb(have, p)
		if deb == "" {
			return fmt.Errorf("no katzenpost package for %s among %d (want %s)",
				p, len(have), debPattern(p))
		}
		if !inPlace {
			if err := copyFile(deb, filepath.Join(r.pkgs, filepath.Base(deb)), 0o644); err != nil {
				return err
			}
		}
		r.log.Logf("  %s runs %s", p, filepath.Base(deb))
	}
	return nil
}

func (r *run) buildRelease(tag string, ds []daemon, plats []platform) ([]string, error) {
	arches := map[string]bool{}
	for _, p := range plats {
		arches[p.debArch()] = true
	}
	var archList []string
	for a := range arches {
		archList = append(archList, a)
	}
	sort.Strings(archList)
	svcs := make([]string, 0, len(ds))
	for _, d := range ds {
		svcs = append(svcs, d.Base)
	}
	r.log.Logf("building the %s katzenpost packages for %d platform(s): %s",
		tag, len(plats), platformList(plats))
	pkgs, err := build.Build(build.Options{
		Repo: r.Repo, Network: r.Network, Tag: tag,
		Dist:        filepath.Join(r.Work, "artifacts"),
		WorkDir:     filepath.Join(r.Work, ".pkgbuild"),
		Arches:      archList,
		Services:    svcs,
		BinaryOnly:  true,
		SkipFreeBSD: true, SkipDocker: true,
		Verbose: r.Verbose,
	})
	if err != nil {
		return nil, fmt.Errorf("kno build (katzenpost packages) failed: %w", err)
	}
	if len(pkgs) == 0 {
		pkgs, _ = filepath.Glob(filepath.Join(r.Work, "artifacts", "releases", tag, "pkgs", "katzenpost_*.deb"))
	}
	return pkgs, nil
}

func platformList(ps []platform) string {
	s := make([]string, 0, len(ps))
	for _, p := range ps {
		s = append(s, p.String())
	}
	return strings.Join(s, " ")
}

func (r *run) debFor(p platform) (string, error) {
	hits, err := filepath.Glob(filepath.Join(r.pkgs, "katzenpost_*.deb"))
	if err != nil {
		return "", err
	}
	if deb := pickDeb(hits, p); deb != "" {
		return filepath.Base(deb), nil
	}
	return "", fmt.Errorf("no katzenpost package for %s in %s (run 'keys' first)", p, r.pkgs)
}

func (r *run) provisionTools() error {
	if err := os.MkdirAll(r.bin, 0o755); err != nil {
		return err
	}
	deb, err := r.debFor(r.clientPlatform())
	if err != nil {
		return err
	}
	ex := filepath.Join(r.Work, ".client-deb")
	if err := os.RemoveAll(ex); err != nil {
		return err
	}
	if out, err := exec.Command("dpkg-deb", "-x", filepath.Join(r.pkgs, deb), ex).CombinedOutput(); err != nil {
		return fmt.Errorf("dpkg-deb -x %s: %v\n%s", deb, err, out)
	}
	gk, err := r.binOf("", roleGenkey)
	if err != nil {
		return err
	}
	if err := copyFile(filepath.Join(ex, gk), filepath.Join(r.bin, "genkeypair"), 0o755); err != nil {
		return fmt.Errorf("the package %s has no %s: %w", deb, gk, err)
	}
	if err := r.buildCPBench(); err != nil {
		return err
	}
	return r.installKno()
}

func (r *run) buildCPBench() error {
	pins, err := r.pins()
	if err != nil {
		return err
	}
	src := filepath.Join(r.Work, ".pkgbuild", ".src")
	if !fsutil.IsDir(filepath.Join(src, "katzenpost", "tools", "chaos", "cmd", "cp-bench")) {
		r.log.Logf("fetching the pinned katzenpost source for cp-bench")
		if _, err := source.MaterializeSource(src, pins,
			filepath.Join(r.Repo, "thirdparty", "patches")); err != nil {
			return fmt.Errorf("materializing the pinned source: %w", err)
		}
	}
	cp := r.clientPlatform()
	img, err := compile.EnsureImage(r.Repo, cp.OS)
	if err != nil {
		return err
	}
	_, cc, err := compile.ArchToGo(cp.debArch())
	if err != nil {
		return err
	}
	mod, gobuild, err := knopath.EnsureCaches()
	if err != nil {
		return err
	}
	r.log.Logf("building cp-bench from the pinned source (%s, in %s)", pins["KATZENPOST_REF"], img)
	out, err := exec.Command(compile.Podman(), "run", "--rm",
		"-v", compile.MustMount(src, "/work:rw"),
		"-v", compile.MustMount(r.bin, "/out:rw"),
		"-v", compile.MustMount(mod, "/gomod:rw"), "-e", "GOMODCACHE=/gomod",
		"-v", compile.MustMount(gobuild, "/gocache:rw"), "-e", "GOCACHE=/gocache",
		"-w", "/work/katzenpost/tools/chaos/cmd/cp-bench",
		"-e", "CGO_ENABLED=1", "-e", "GOOS=linux", "-e", "GOARCH="+cp.Goarch,
		"-e", "CC="+cc, "-e", "GOWORK=/work/go.work",
		"-e", "GIT_CONFIG_COUNT=1", "-e", "GIT_CONFIG_KEY_0=safe.directory",
		"-e", "GIT_CONFIG_VALUE_0=*",
		img, "go", "build", "-trimpath", "-o", "/out/cp-bench", ".").CombinedOutput()
	if err != nil {
		return fmt.Errorf("cp-bench build failed: %v\n%s", err, out)
	}
	return nil
}

func (r *run) installKno() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	want, err := os.ReadFile(exe)
	if err != nil {
		return err
	}
	dst := filepath.Join(r.bin, "kno")
	if have, err := os.ReadFile(dst); err == nil && bytes.Equal(have, want) {
		return nil
	}
	r.log.Logf("using this kno inside the sim: %s", exe)
	if err := os.MkdirAll(r.bin, 0o755); err != nil {
		return err
	}
	return copyFile(exe, dst, 0o755)
}

func (r *run) keysStage() error {
	if err := os.MkdirAll(r.Work, 0o755); err != nil {
		return err
	}
	if err := r.provisionPackages(); err != nil {
		return err
	}
	if err := r.provisionTools(); err != nil {
		return err
	}
	r.log.Logf("generating a fresh keyset for %s", r.Network)
	if err := r.regenNetworkKeys(); err != nil {
		return err
	}
	n, err := countDirs(r.dd)
	if err != nil {
		return err
	}
	r.log.Logf("seeded %d node datadirs", n)
	return nil
}
