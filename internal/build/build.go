package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/katzenpost/kno/internal/argcheck"
	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/knover"
	"github.com/katzenpost/kno/internal/parallel"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/pkg/freebsd"
	"github.com/katzenpost/kno/internal/pkg/hostconfig"
	"github.com/katzenpost/kno/internal/pkg/knodeb"
	"github.com/katzenpost/kno/internal/release"
	"github.com/katzenpost/kno/internal/service"
	"github.com/katzenpost/kno/internal/source"
)

type Options struct {
	Repo, Network, Tag, Dist string
	Arches                   []string
	Services                 []string
	Peers                    map[string]string
	Warped, Offline          bool

	Force bool

	Content map[deb.PkgID]string

	Revision string
	Image    string

	ToolRevision string

	Hosts  []string
	Config *config.Config

	WorkDir string

	SkipCompile bool

	BinaryOnly bool

	SkipFreeBSD, SkipDocker bool

	SkipDeb bool

	SkipTarball bool

	Verbose bool

	Native bool

	Qemu bool
}

func (o Options) kind() string {
	if o.Native || o.Qemu || runtime.GOARCH != "amd64" {
		return NativeBuild
	}
	for _, pf := range o.platforms() {
		if pf.Arch != "amd64" {
			return CrossBuild
		}
	}
	return NativeBuild
}

func upstreamWork(o Options, pf compile.Platform) string {
	return filepath.Join(o.work(), "upstream-"+pf.Arch+"-"+compile.OSToken(pf.OS))
}

func (o Options) work() string {
	if o.WorkDir != "" {
		return o.WorkDir
	}
	return filepath.Join(knopath.Cache(), "build")
}

func lockWork(work string) (func(), error) {
	return fsutil.Lock(filepath.Join(work, ".lock"), func() {
		fmt.Fprintf(os.Stderr, "kno build: another kno build uses %s. kno waits for it\n", work)
	})
}

func (o Options) toolRevision() string {
	if o.ToolRevision != "" {
		return o.ToolRevision
	}

	if h, ok := release.Read(o.Repo, o.Network, o.Tag); ok && h.ToolRevision != "" {
		return h.ToolRevision
	}
	if o.Revision != "" {
		return o.Revision
	}
	return knover.Revision(o.Repo, o.Tag)
}

func (o Options) hostConfigPkgs(version, outdir, abi string) ([]string, error) {
	if len(o.Hosts) == 0 || o.Config == nil {
		if o.SkipDeb {
			return nil, nil
		}
		p, err := hostconfig.Package(context.Background(), o.Repo, o.Network, version, outdir)
		if err != nil {
			return nil, err
		}
		return []string{p}, nil
	}
	out := make([]string, 0, len(o.Hosts))
	for _, h := range o.Hosts {
		if hc, ok := o.Config.Hosts[h]; ok && service.AllEzjail(o.Repo, o.Network, config.HostServices(o.Repo, *o.Config, hc)) {
			if o.SkipFreeBSD {
				continue
			}
			epoch, err := deb.Epoch(o.Repo, o.Tag)
			if err != nil {
				return nil, err
			}
			p, err := freebsd.HostConfigPackage(*o.Config, h, version, outdir, abi, o.Repo, o.Network, epoch)
			if err != nil {
				return nil, fmt.Errorf("host %s: %w", h, err)
			}
			out = append(out, p)
			continue
		}
		p, err := hostconfig.PackageForHost(context.Background(), *o.Config, h, version,
			outdir, o.Repo, o.Network, o.hostArch(h))
		if err != nil {
			return nil, fmt.Errorf("host %s: %w", h, err)
		}
		out = append(out, p)
	}
	return out, nil
}

func (o Options) hostArch(label string) string {
	if o.Config == nil {
		return deb.ArchAll
	}
	h, ok := o.Config.Hosts[label]
	if !ok {
		return deb.ArchAll
	}
	for _, svc := range config.HostServices(o.Repo, *o.Config, h) {
		n, _ := config.ServiceNode(o.Repo, o.Network, svc)
		if a := config.NodeArch(o.Repo, o.Network, n); a != "" {
			return a
		}
	}
	return deb.ArchAll
}

type contentIDs struct {
	mu sync.Mutex
	m  map[deb.PkgID]string
}

func (c *contentIDs) note(pkg deb.PkgID, id string) {
	if id == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[deb.PkgID]string{}
	}
	c.m[pkg] = id
}

func (c *contentIDs) all() map[deb.PkgID]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m
}

func (o Options) serviceTarget(svc string) (string, string) {
	node, _ := config.ServiceNode(o.Repo, o.Network, svc)
	return config.NodeArch(o.Repo, o.Network, node), o.nodeOS(node)
}

func (o Options) nodeOS(node string) string {
	if v := config.NodeOS(o.Repo, o.Network, node); v != "" {
		return v
	}
	return compile.DefaultOS
}

func (o Options) platforms() []compile.Platform {
	if o.SkipDeb {
		return nil
	}
	want := map[compile.Platform]bool{}
	arches := map[string]bool{}
	for _, a := range o.Arches {
		arches[a] = true
	}
	covered := map[string]bool{}
	for _, svc := range o.Services {
		if !BackendFormats(o.Repo, o.Network, []string{svc})["deb"] {
			continue
		}
		node, _ := config.ServiceNode(o.Repo, o.Network, svc)
		a := config.NodeArch(o.Repo, o.Network, node)
		if a == "" || !arches[a] {
			continue
		}
		want[compile.Platform{Arch: a, OS: o.nodeOS(node)}] = true
		covered[a] = true
	}
	for _, a := range o.Arches {
		if !covered[a] {
			want[compile.Platform{Arch: a, OS: compile.DefaultOS}] = true
		}
	}
	out := make([]compile.Platform, 0, len(want))
	for p := range want {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Arch != out[j].Arch {
			return out[i].Arch < out[j].Arch
		}
		return out[i].OS < out[j].OS
	})
	return out
}

func binDir(work string, p compile.Platform) string {
	return filepath.Join(work, "bin-"+p.Arch+"-"+compile.OSToken(p.OS))
}

func (o Options) PrepareImages(w io.Writer) error {
	pins, err := source.ParsePins(filepath.Join(o.Repo, "thirdparty", "pins.env"))
	if err != nil {
		return err
	}
	pfs := o.platforms()
	kind := o.kind()
	for _, pf := range pfs {
		u := upstreamDeb{Repo: o.Repo, Work: upstreamWork(o, pf), Arch: pf.Arch, OS: pf.OS,
			Pins: pins, Out: w, Verbose: o.Verbose,
			Cross: kind == CrossBuild && pf.Arch != "amd64"}
		if _, err := u.image(context.Background()); err != nil {
			return err
		}
	}
	fmt.Fprintf(w, "kno build: %d build image(s) ready\n", len(pfs))
	return nil
}

func (o Options) stampIn(kno string) stampInputs {
	platforms := o.platforms()
	pfKeys := make([]string, len(platforms))
	for i, pf := range platforms {
		pfKeys[i] = pf.String()
	}
	return stampInputs{
		Tag: o.Tag, Revision: o.Revision, ToolRevision: o.toolRevision(),
		KnoVersion: deb.ToolVersionRev(o.Repo, o.Tag, o.toolRevision()),
		Network:    o.Network,
		Arches:     pfKeys, Services: o.Services, Repo: o.Repo,
		BinaryOnly: o.BinaryOnly, Warped: o.Warped, Kno: kno,
		Epoch: epochFor(o), RecordPins: release.Pins(o.Repo, o.Network, o.Tag),
		Peers: o.Peers, HostConfigs: o.hostConfigDigests(),
		Kind: o.kind(),
	}
}

func (o Options) hostConfigDigests() map[string]string {
	if o.Config == nil || len(o.Hosts) == 0 {
		return nil
	}
	out := make(map[string]string, len(o.Hosts))
	for _, h := range o.Hosts {
		b, err := json.Marshal(hostconfig.ForHost(*o.Config, h, o.Repo))
		if err != nil {
			return nil
		}
		sum := sha256.Sum256(b)
		out[h] = hex.EncodeToString(sum[:])
	}
	return out
}

func (o Options) cacheKey(rel, kno string) string { return stamp(o.stampIn(kno)) }

func MarkBuiltFor(o Options, kno string) error {
	rel := filepath.Join(o.Dist, "releases", o.Tag)
	if rev, _ := revisionFor(o.Revision, rel,
		CommittedDir(o.Repo, o.Network, o.Tag), o.Tag); rev != "" {
		o.Revision = rev
	}
	key := o.cacheKey(rel, kno)
	if key == "" {
		return fmt.Errorf("no build key for %s with %s", o.Tag, kno)
	}
	return writeStampNote(rel, key, stampNote(o.stampIn(kno), o.Hosts))
}

var ErrNotBuilt = errors.New("not built")

var verbose bool

var stepOut io.Writer = os.Stderr

func version(tag string) string { return strings.TrimPrefix(tag, "v") }

func freebsdTarget(abi string) string {
	parts := strings.Split(abi, ":")
	if len(parts) != 3 {
		return abi
	}
	return parts[2] + " on freebsd-" + parts[1]
}

func step(format string, a ...any) {
	fmt.Fprintf(stepOut, "kno build: "+format+"\n", a...)
}

func note(format string, a ...any) {
	if verbose {
		fmt.Fprintf(os.Stderr, "kno build: "+format+"\n", a...)
	}
}

func built(rel, key string, in stampInputs, hosts []string) []string {
	if pkgs := cached(rel, key); pkgs != nil {
		return pkgs
	}
	return covering(rel, in, hosts)
}

func Ready(o Options) ([]string, error) {
	verbose = o.Verbose
	rel := filepath.Join(o.Dist, "releases", o.Tag)
	if rev, _ := revisionFor(o.Revision, rel,
		CommittedDir(o.Repo, o.Network, o.Tag), o.Tag); rev != "" {
		o.Revision = rev
	}
	in := o.stampIn("")
	pkgs := built(rel, stamp(in), in, o.Hosts)
	if pkgs == nil {
		return nil, ErrNotBuilt
	}
	note("%s is built for these hosts. The %d packages on disk match the digests in the release directory.", o.Tag, len(pkgs))
	return pkgs, verifyAgainstRecord(o, rel)
}

func checkServices(repo, network string, svcs []string) error {
	for _, svc := range svcs {
		if err := argcheck.PackageName("service", svc); err != nil {
			return err
		}
		if _, err := service.ResolveService(repo, network, svc); err != nil {
			return err
		}
	}
	return nil
}

func Build(o Options) ([]string, error) {
	verbose = o.Verbose
	if err := checkServices(o.Repo, o.Network, o.Services); err != nil {
		return nil, err
	}
	if o.Warped {
		return nil, fmt.Errorf("katzenpost %s is built with its own debian/ packaging, which"+
			" has no warped flavour. Set the epoch when the daemons run"+
			" (KATZENPOST_EPOCH_DURATION, or EpochDuration in the config)", o.Tag)
	}
	if o.Image == "" {
		o.Image = "kno-linux-build"
	}

	epoch := epochFor(o)
	if err := os.Setenv("SOURCE_DATE_EPOCH", epoch); err != nil {
		return nil, err
	}

	rel := filepath.Join(o.Dist, "releases", o.Tag)

	unlock, err := lockWork(o.work())
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", o.work(), err)
	}
	defer unlock()

	if rev, why := revisionFor(o.Revision, rel,
		CommittedDir(o.Repo, o.Network, o.Tag), o.Tag); rev != "" {
		o.Revision = rev
		if why != "" {
			note("%s", why)
		}
	}

	platforms := o.platforms()
	in := o.stampIn("")
	key, scope := stamp(in), stampNote(in, o.Hosts)
	if !o.Force {
		if pkgs := built(rel, key, in, o.Hosts); pkgs != nil {
			note("%s is already built from the same inputs. The %d packages on disk match the digests"+
				" in the release directory. kno build did not build again. To build again, add --force.", o.Tag, len(pkgs))
			if err := verifyAgainstRecord(o, rel); err != nil {
				return nil, err
			}
			return pkgs, nil
		}
	}

	if o.BinaryOnly {
		if err := os.MkdirAll(rel, 0o755); err != nil {
			return nil, err
		}
	} else if err := release.Stage(o.Repo, o.Network, rel, o.Services, o.Peers); err != nil {
		return nil, fmt.Errorf("stage: %w", err)
	}

	pins, err := source.ParsePins(filepath.Join(o.Repo, "thirdparty", "pins.env"))
	if err != nil {
		return nil, fmt.Errorf("pins: %w", err)
	}

	if ref := pins["KATZENPOST_REF"]; ref != "" && o.Tag != "" &&
		release.Pins(o.Repo, o.Network, o.Tag) == nil {
		if want, _ := deb.SplitRevision(o.Tag); want != ref {
			return nil, fmt.Errorf("the katzenpost source is %s (data/thirdparty/pins.env)."+
				" You asked for %s.\n  kno knows no commit for %s, so the packages would"+
				" have the name %s and the code of %s.\n  To set the source to the newest"+
				" release tag, run kno build pin tag. To build %s, run kno build without --tag.",
				ref, want, want, want, ref, ref)
		}
	}
	if rec := release.Pins(o.Repo, o.Network, o.Tag); rec != nil {
		for k, v := range rec {
			if pins[k] != v {
				fmt.Fprintf(os.Stderr, "kno build: The committed release %s sets %s=%s."+
					" data/thirdparty/pins.env sets %s. kno build uses the committed value.\n",
					o.Tag, k, v, pins[k])
			}
			pins[k] = v
		}
	}
	work := filepath.Join(o.work(), ".src")

	if o.Native {
		if !o.SkipCompile {
			step("preparing the source of katzenpost %s and hpqc %s", version(o.Tag),
				version(pins["HPQC_REF"]))
			if _, err := source.MaterializeSource(work, pins, filepath.Join(o.Repo, "thirdparty", "patches")); err != nil {
				return nil, fmt.Errorf("materialize source: %w", err)
			}
		}
		ids := &contentIDs{}
		pkgs, err := buildNative(o, rel, work, pins, ids)
		if err != nil {
			return nil, err
		}
		return finish(o, rel, key, scope, pkgs, ids)
	}
	built := make([]string, len(platforms))
	if !o.SkipCompile {
		if err := needEngine(); err != nil {
			return nil, err
		}
		step("preparing the source of katzenpost %s and hpqc %s", version(o.Tag),
			version(pins["HPQC_REF"]))
		if _, err := source.MaterializeSource(work, pins, filepath.Join(o.Repo, "thirdparty", "patches")); err != nil {
			return nil, fmt.Errorf("materialize source: %w", err)
		}
		modCache, buildCache, err := knopath.EnsureCaches()
		if err != nil {
			return nil, err
		}
		recorded := map[string]string{}
		kind := o.kind()
		entries, _ := committedEntries(o.Repo, o.Network, o.Tag, kind)
		for _, e := range entries {
			recorded[e.Name] = e.Sha256
		}
		tasks := make([]parallel.Task, len(platforms))
		for i, pf := range platforms {
			i, pf := i, pf
			tasks[i] = func(ctx context.Context) error {
				if d := reusableUpstream(o, rel, pf, recorded); d != "" {
					note("katzenpost %s for %s matches the record. kno uses %s again", o.Tag, pf, filepath.Base(d))
					built[i] = d
					return nil
				}
				step("building katzenpost %s for %s on %s", version(o.Tag),
					pf.Arch, pf.OS)
				d, err := upstreamDeb{
					Repo: o.Repo, Src: filepath.Join(work, "katzenpost"), Work: upstreamWork(o, pf), Tag: o.Tag,
					Arch: pf.Arch, OS: pf.OS, Pins: pins,
					ModCache: modCache, GoCache: buildCache, Out: os.Stderr, Verbose: o.Verbose,
					Offline: o.Offline, Cross: kind == CrossBuild && pf.Arch != "amd64",
				}.Build(ctx)
				if err != nil {
					return err
				}
				built[i] = d
				return nil
			}
		}
		if len(tasks) > 0 {
			if err := parallel.Run(min(parallel.Jobs(), len(tasks)), tasks); err != nil {
				return nil, err
			}
		}
	} else {
		for i, pf := range platforms {
			d, err := builtDeb(filepath.Join(upstreamWork(o, pf), "tree"), pf.Arch)
			if err != nil {
				return nil, err
			}
			built[i] = d
		}
	}

	ids := &contentIDs{}
	pkgs := make([]string, len(platforms))
	for i, pf := range platforms {
		dst, err := placeUpstream(built[i], filepath.Join(rel, "pkgs"), o.Tag, pf.OS, pf.Arch, binDir(o.work(), pf))
		if err != nil {
			return nil, fmt.Errorf("katzenpost deb (%s): %w", pf, err)
		}
		if !o.SkipCompile {
			tree := filepath.Join(upstreamWork(o, pf), "tree")
			if err := os.RemoveAll(tree); err != nil {
				return nil, err
			}
		}
		sum, _, err := fsutil.SHA256File(dst)
		if err != nil {
			return nil, err
		}
		ids.note(deb.PkgID{Name: "katzenpost", Arch: pf.Arch, OS: compile.OSToken(pf.OS)}, sum)
		pkgs[i] = dst
	}

	stable := NewStablePreferring(
		Recorded(CommittedDir(o.Repo, o.Network, o.Tag)),
		Existing(filepath.Join(rel, "pkgs")))

	if o.ToolRevision != "" {
		stable = nil
	}
	if o.BinaryOnly {
		return pkgs, nil
	}

	var cfg []string
	if !o.SkipDeb {
		cfg, err = deb.PackageConfigsFlavoured(context.Background(), rel, o.Tag, o.toolRevision(), o.Revision,
			o.serviceTarget, func(id deb.PkgID, content, want string) string {
				ids.note(id, content)
				v, why := stable.Version(id, content, want)
				if why != "" {
					note("%s", why)
				}
				return v
			},
			func(svc string) string { return deb.ConfigStamp(o.Repo, o.Network, svc) },
			func(svc string) bool { return debService(o.Repo, o.Network, svc) })
		if err != nil {
			return nil, fmt.Errorf("config debs: %w", err)
		}
	}

	if err := retireAll(append(append([]string{}, pkgs...), cfg...), o.Tag); err != nil {
		return nil, err
	}
	pkgs = append(pkgs, cfg...)

	toolVer := deb.ToolVersionRev(o.Repo, o.Tag, o.toolRevision())

	if !o.SkipCompile && !o.SkipDeb {
		arches := o.Arches
		if len(arches) == 0 {
			arches = []string{compile.HostDebArch()}
		}
		for _, arch := range arches {
			dst, err := knoDeb(o, rel, toolVer, arch)
			if err != nil {
				return nil, err
			}
			pkgs = append(pkgs, dst)
		}
	}

	hcs, err := o.hostConfigPkgs(toolVer, filepath.Join(o.work(), "hostcfg"), pins["FREEBSD_ABI"])
	if err != nil {
		return nil, fmt.Errorf("host-config deb: %w", err)
	}
	for _, hc := range hcs {
		dst, err := place(hc, filepath.Join(rel, "pkgs"), o.Tag)
		if err != nil {
			return nil, err
		}
		pkgs = append(pkgs, dst)
	}

	if !o.SkipFreeBSD && !o.SkipCompile {
		step("building katzenpost %s for %s", version(o.Tag),
			freebsdTarget(pins["FREEBSD_ABI"]))
		sysroot := filepath.Join(o.work(), "fbsd", "sysroot")
		bindir := filepath.Join(o.work(), "fbsd", "bin")
		modCache, buildCache, err := knopath.EnsureCaches()
		if err != nil {
			return nil, err
		}
		caches := freebsd.Caches{Mod: modCache, Build: buildCache, Offline: o.Offline}
		if err := freebsd.CrossCompile(o.Repo, work, bindir, sysroot, o.Tag, pins, caches); err != nil {
			return nil, fmt.Errorf("freebsd cross build: %w", err)
		}
		fp, err := freebsd.PackageStamped(bindir, rel, o.Tag, o.Revision, o.Repo, pins["FREEBSD_ABI"], deb.ConfigStamp(o.Repo, o.Network, "client"))
		if err != nil {
			return nil, fmt.Errorf("freebsd pkg: %w", err)
		}
		pkgs = append(pkgs, fp)
		kp, err := knoPkg(o, rel, toolVer, pins, false)
		if err != nil {
			return nil, err
		}
		pkgs = append(pkgs, kp)
		cfgs, err := freebsdConfigPkgs(o, rel, pins["FREEBSD_ABI"], ids)
		if err != nil {
			return nil, err
		}
		if err := retireAll(append([]string{fp, kp}, cfgs...), o.Tag); err != nil {
			return nil, err
		}
		pkgs = append(pkgs, cfgs...)
	}

	if !o.SkipTarball && !o.SkipCompile {
		abi := ""
		if !o.SkipFreeBSD {
			abi = pins["FREEBSD_ABI"]
		}
		tb, err := tarballs(tarballOpts{
			Repo: o.Repo, Network: o.Network, Tag: o.Tag,
			Work: o.work(), Rel: rel, Platforms: o.platforms(),
			FreeBSDABI: abi, Source: true, Pins: pins, ToolVer: toolVer,
		})
		if err != nil {
			return nil, fmt.Errorf("tarballs: %w", err)
		}
		pkgs = append(pkgs, tb...)
	}

	return finish(o, rel, key, scope, pkgs, ids)
}

func finish(o Options, rel, key string, scope []byte, pkgs []string, ids *contentIDs) ([]string, error) {
	if !o.BinaryOnly {
		if err := release.WriteStagedSums(rel); err != nil {
			return nil, fmt.Errorf("staged record: %w", err)
		}
	}

	o.Content = ids.all()
	if err := recordAndVerify(o, pkgs); err != nil {
		return nil, err
	}
	if err := verifyAgainstRecord(o, rel); err != nil {
		return nil, err
	}

	if err := writeStampNote(rel, key, scope); err != nil {
		return nil, err
	}
	return pkgs, nil
}

func epochFor(o Options) string {
	if v := deb.EnvSettings().SourceDateEpoch; v != "" {
		return v
	}
	if e := CommittedEpoch(o.Repo, o.Network, o.Tag); e != "" {
		return e
	}
	return deb.SourceDateEpoch(o.Repo, o.Tag)
}

func reusableUpstream(o Options, rel string, pf compile.Platform, recorded map[string]string) string {
	if o.Force || o.ToolRevision != "" {
		return ""
	}
	name := "katzenpost_" + deb.OSVersion(o.Tag, "", pf.OS) + "_" + pf.Arch + ".deb"
	want, ok := recorded[name]
	if !ok {
		return ""
	}
	p := filepath.Join(rel, "pkgs", name)
	if got, _, err := fsutil.SHA256File(p); err != nil || got != want {
		return ""
	}
	return p
}

func needEngine() error {
	if _, err := exec.LookPath(compile.Podman()); err != nil {
		return fmt.Errorf("kno build compiles katzenpost in containers, and %s is not"+
			" usable here: %v\n"+
			"  Install podman. On macOS, then run: podman machine init; podman machine start\n"+
			"  On FreeBSD, kno build --native compiles without a container",
			compile.Podman(), err)
	}
	return nil
}

func verifyAgainstRecord(o Options, rel string) error {
	if o.Warped || o.BinaryOnly || o.SkipCompile {
		return nil
	}
	d, err := DiffAgainstCommitted(o.Repo, o.Network, o.Tag,
		filepath.Join(rel, "pkgs"), "")
	if errors.Is(err, ErrNoCommittedSums) {
		fmt.Fprintf(os.Stderr, "kno build: The repository has no record"+
			" of %s. kno compared this build only with itself.\n", o.Tag)
		fmt.Fprintf(os.Stderr, "kno build:   To make one, run kno build record and"+
			" commit the result.\n")
		return nil
	}
	if err != nil {
		return err
	}
	if len(d.Mismatched) > 0 {
		return fmt.Errorf("this build does not match the record of %s (%s):\n"+
			"  %s\n"+
			"  A package has the same name and different content. Usually an input\n"+
			"  changed after the commit of the record, for example a config or\n"+
			"  the katzenpost source in data/thirdparty/pins.env. In that case, make\n"+
			"  a new revision. Use a kno revision higher than the one in the record:\n"+
			"    kno build --tool-revision <n>\n"+
			"    kno build record\n"+
			"  If no input changed, this host did not reproduce the build.\n"+
			"  To test if this host can reproduce a build, run: kno check repro",
			o.Tag, CommittedDir(o.Repo, o.Network, o.Tag), strings.Join(d.Mismatched, "\n  "))
	}
	if len(d.Unnamed) > 0 {
		step("the record of katzenpost %s has no entry for %s. To add them,"+
			" run kno build record, and commit the result", version(o.Tag),
			strings.Join(d.Unnamed, ", "))
		return nil
	}
	step("the build matches the record of katzenpost %s", version(o.Tag))
	return nil
}

func buildNative(o Options, rel, work string, pins map[string]string, ids *contentIDs) ([]string, error) {
	if runtime.GOOS != "freebsd" {
		return nil, fmt.Errorf("--native is only supported on freebsd, not %s", runtime.GOOS)
	}
	bindir := filepath.Join(o.work(), "bin-native")
	jobs := parallel.Jobs()
	gmp := 1
	tasks, err := freebsd.NativeTasks(work, bindir, o.Tag, pins["GO_VERSION"], &gmp)
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("no katzenpost cmd dirs under %s", work)
	}
	gmp = parallel.GOMAXPROCS(jobs, len(tasks))
	note("compiling %d binaries natively, %d parallel, GOMAXPROCS=%d", len(tasks), min(jobs, len(tasks)), gmp)
	if err := parallel.Run(jobs, tasks); err != nil {
		return nil, err
	}
	pkg, err := freebsd.PackageStamped(bindir, rel, o.Tag, o.Revision, o.Repo, pins["FREEBSD_ABI"], deb.ConfigStamp(o.Repo, o.Network, "client"))
	if err != nil {
		return nil, fmt.Errorf("freebsd pkg: %w", err)
	}
	out := []string{pkg}
	if o.BinaryOnly {
		return out, nil
	}
	cfgs, err := freebsdConfigPkgs(o, rel, pins["FREEBSD_ABI"], ids)
	if err != nil {
		return nil, err
	}
	out = append(out, cfgs...)
	toolVer := deb.ToolVersionRev(o.Repo, o.Tag, o.toolRevision())
	kp, err := knoPkg(o, rel, toolVer, pins, true)
	if err != nil {
		return nil, err
	}
	out = append(out, kp)
	if len(o.Hosts) > 0 && o.Config != nil {
		hcs, err := o.hostConfigPkgs(toolVer, filepath.Join(rel, "pkgs"), pins["FREEBSD_ABI"])
		if err != nil {
			return nil, fmt.Errorf("host-config pkg: %w", err)
		}
		out = append(out, hcs...)
	}
	return out, retireAll(out, o.Tag)
}

func knoPkg(o Options, rel, toolVer string, pins map[string]string, local bool) (string, error) {
	ko, err := knodeb.DefaultOptions(o.Repo)
	if err != nil {
		return "", fmt.Errorf("kno pkg: %w", err)
	}
	ko.Version, ko.Verbose, ko.OS, ko.Arch = toolVer, o.Verbose, "freebsd", "amd64"
	step("building kno %s for %s", toolVer, freebsdTarget(pins["FREEBSD_ABI"]))
	bindir := filepath.Join(o.work(), "kno-freebsd")
	if err := os.RemoveAll(bindir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(bindir, 0o755); err != nil {
		return "", err
	}
	if local {
		err = knodeb.CompileLocal(context.Background(), ko, pins["GO_VERSION"], filepath.Join(bindir, "kno"))
	} else {
		err = knodeb.Compile(context.Background(), ko, bindir)
	}
	if err != nil {
		return "", err
	}
	return knoPkgOf(o, filepath.Join(bindir, "kno"), rel, toolVer, pins["FREEBSD_ABI"])
}

func knoPkgOf(o Options, bin, rel, toolVer, abi string) (string, error) {
	epoch, err := deb.Epoch(o.Repo, o.Tag)
	if err != nil {
		return "", err
	}
	p, err := freebsd.KnoPackage(bin, toolVer, filepath.Join(rel, "pkgs"), abi, epoch)
	if err != nil {
		return "", fmt.Errorf("kno pkg: %w", err)
	}
	return p, nil
}

func freebsdConfigPkgs(o Options, rel, abi string, ids *contentIDs) ([]string, error) {
	stable := NewStablePreferring(
		Recorded(CommittedDir(o.Repo, o.Network, o.Tag)),
		Existing(filepath.Join(rel, "pkgs")))
	if o.ToolRevision != "" {
		stable = nil
	}
	version := func(svc string) func(content, want string) string {
		return func(content, want string) string {
			id := deb.PkgID{Name: freebsd.ConfigPkgName(svc), OS: "freebsd"}
			ids.note(id, content)
			v, why := stable.Version(id, content, want)
			if why != "" {
				note("%s", why)
			}
			return v
		}
	}
	var out []string
	for _, svc := range o.Services {
		rs, err := service.ResolveService(o.Repo, o.Network, svc)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", svc, err)
		}
		if rs.Backend != "freebsd-ezjail" {
			continue
		}
		s := freebsd.SvcOf(rs, deb.ConfigStamp(o.Repo, o.Network, svc))
		s.BinaryStamp = deb.ConfigStamp(o.Repo, o.Network, "client")
		cp, err := freebsd.PackageConfig(rel, o.Tag, o.Revision, svc, s, o.Repo, abi, version(svc))
		if err != nil {
			return nil, fmt.Errorf("freebsd config pkg: %w", err)
		}
		out = append(out, cp)
	}
	return out, nil
}

func gcServices(repo, network string) []string {
	gc := knopath.ConfigsDir(repo, network)
	files, _ := filepath.Glob(filepath.Join(gc, "*-pq-*.toml"))
	var out []string
	for _, f := range files {
		b := strings.TrimSuffix(filepath.Base(f), ".toml")
		switch {
		case strings.HasSuffix(b, "-pq-authority"),
			strings.HasSuffix(b, "-pq-mixserver"),
			strings.HasSuffix(b, "-pq-gatewaynode"),
			strings.HasSuffix(b, "-pq-servicenode"),
			strings.HasSuffix(b, "-pq-storagenode"):
			out = append(out, b)
		default:
			if i := strings.Index(b, "-pq-storagenode-"); i >= 0 {
				out = append(out, b)
			}
		}
	}
	sort.Strings(out)
	return out
}

func ArchesForServices(repo, network string, svcs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, svc := range svcs {
		n, _ := config.ServiceNode(repo, network, svc)
		a := config.NodeArch(repo, network, n)
		if a != "" && !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out
}

func splitArches(s string) []string {
	f := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })
	if len(f) == 0 {
		return []string{"amd64", "arm64"}
	}
	return f
}

func place(src, dir, tag string) (string, error) {
	dst := filepath.Join(dir, filepath.Base(src))
	if err := fsutil.CopyFile(src, dst); err != nil {
		return "", err
	}
	note("packaged %s", filepath.Base(dst))
	for _, old := range superseded(dst) {
		where, err := archive(old, tag)
		if err != nil {
			return "", err
		}
		note("archived %s (superseded) -> %s", filepath.Base(old), where)
	}
	return dst, nil
}

func retireAll(pkgs []string, tag string) error {
	for _, p := range pkgs {
		if p == "" {
			continue
		}
		for _, old := range superseded(p) {
			where, err := archive(old, tag)
			if err != nil {
				return err
			}
			note("archived %s (superseded) -> %s", filepath.Base(old), where)
		}
	}
	return nil
}

func archive(pkg, tag string) (string, error) {
	dir := knopath.Archive(tag)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, filepath.Base(pkg))
	if _, err := os.Stat(dst); err == nil {

		if err := os.Remove(pkg); err != nil && !os.IsNotExist(err) {
			return "", err
		}
		return dir, nil
	}
	if err := os.Rename(pkg, dst); err == nil {
		return dir, nil
	}

	if err := fsutil.CopyFile(pkg, dst); err != nil {
		return "", err
	}
	if err := os.Remove(pkg); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return dir, nil
}

func supersededPkg(pkg string) []string {
	name, ok := freebsd.SplitPkgName(filepath.Base(pkg))
	if !ok {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(pkg), name+"-*.pkg"))
	var out []string
	for _, m := range matches {
		if n, ok := freebsd.SplitPkgName(filepath.Base(m)); ok && n == name && m != pkg {
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out
}

func superseded(pkg string) []string {
	if strings.HasSuffix(pkg, ".pkg") {
		return supersededPkg(pkg)
	}
	name, arch, ok := splitDebName(filepath.Base(pkg))
	if !ok {
		return nil
	}
	osToken := debOSToken(filepath.Base(pkg))
	arches := []string{arch}
	if arch != deb.ArchAll {
		arches = append(arches, deb.ArchAll)
	}
	var out []string
	for _, a := range arches {
		matches, err := filepath.Glob(filepath.Join(filepath.Dir(pkg), name+"_*_"+a+".deb"))
		if err != nil {
			return nil
		}
		for _, m := range matches {
			if m == pkg {
				continue
			}

			n, ma, ok := splitDebName(filepath.Base(m))
			if ok && n == name && ma == a && debOSToken(filepath.Base(m)) == osToken {
				out = append(out, m)
			}
		}
	}
	sort.Strings(out)
	return out
}

func debOSToken(base string) string { return deb.OSTokenOf(base) }

func splitDebName(base string) (name, arch string, ok bool) { return deb.SplitName(base) }

func NetworkServices(repo, network string) []string { return gcServices(repo, network) }
