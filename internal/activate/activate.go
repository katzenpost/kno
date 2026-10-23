package activate

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/build"
	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/pkg/freebsd"
	"github.com/katzenpost/kno/internal/release"
	"github.com/katzenpost/kno/internal/service"
	"github.com/katzenpost/kno/internal/sign"
	"github.com/katzenpost/kno/internal/track"

	"github.com/katzenpost/kno/internal/knopath"
)

const configPkgPrefix = knopath.ConfigPkgPrefix

type StepKind int

const (
	StepRun StepKind = iota

	StepRunIgnore

	StepScanSecretKeys

	StepWrite
)

type Step struct {
	Kind StepKind
	Argv []string

	Dir string

	Log string

	Path    string
	Content string
	Mode    os.FileMode
}

type Plan struct {
	Backend string
	Tag     string

	Needs string

	Steps []Step

	Packages []string
}

func (p Plan) Argv() [][]string {
	out := make([][]string, 0, len(p.Steps))
	for _, s := range p.Steps {
		out = append(out, s.Argv)
	}
	return out
}

type Options struct {
	Rel, Tag string

	Services []string

	Arch string

	OS string

	Backend string

	Install string

	Repo string

	Network string

	ClientJail string

	WantClient bool

	DryRun bool

	Allowlist string

	Threshold int
}

func (o Options) allowlist() string {
	if o.Allowlist == "" {
		return sign.NodePath
	}
	return o.Allowlist
}

func PlanFor(o Options) (Plan, error) {

	if !isDir(filepath.Join(o.Rel, "manifest")) && !isDir(filepath.Join(o.Rel, "pkgs")) {
		return Plan{}, fmt.Errorf("not a release dir: %s (no pkgs/ and no manifest/)", o.Rel)
	}
	if len(o.Services) == 0 {
		return Plan{}, fmt.Errorf("no services resolve for this machine; run 'kno setup'" +
			" (or install kno-host-config), or deploy to a node with 'kno push <host> <tag>'")
	}
	backend := o.Backend
	if backend == "" {
		backend = ReleaseBackend(o.Rel)
	}
	switch backend {
	case "systemd":
		return systemdPlan(o, backend)
	case "docker":
		return dockerPlan(o, backend)
	case "freebsd-ezjail":
		return ezjailPlan(o, backend)
	}
	return Plan{}, fmt.Errorf("unknown backend: %s", backend)
}

func systemdPlan(o Options, backend string) (Plan, error) {
	arch := o.Arch
	if arch == "" {
		arch = "amd64"
	}
	ver := DebVersion(o.Tag)
	pkgs := filepath.Join(o.Rel, "pkgs")

	kp := ""
	for _, v := range []string{deb.OSVersion("v"+ver, "", o.OS), ver} {
		c := filepath.Join(pkgs, "katzenpost_"+v+"_"+arch+".deb")
		if readable(c) {
			kp = c
			break
		}
	}
	if kp == "" {
		return Plan{}, fmt.Errorf("missing binary package: no katzenpost_%s_%s.deb"+
			" in %s (built for %s?)", ver, arch, pkgs, orUnknown(o.OS))
	}

	debs := []string{kp}
	for _, svc := range o.Services {
		c, err := configPkg(pkgs, svc, ver, arch)
		if err != nil {
			return Plan{}, err
		}
		debs = append(debs, c)
	}

	p := Plan{Backend: backend, Tag: o.Tag, Needs: "dpkg", Packages: debs}
	for _, d := range debs {
		p.Steps = append(p.Steps, Step{Kind: StepScanSecretKeys, Argv: []string{"dpkg-deb", "--fsys-tarfile", d}})
	}

	fresh := notInstalled(debs)
	switch {
	case len(fresh) == 0:
	case fresh[0] == kp:
		p.Steps = append(p.Steps, installStep(o, fresh), kpclientdStep(o.WantClient))
	default:
		p.Steps = append(p.Steps, installStep(o, fresh))
	}
	if len(fresh) == 0 || fresh[0] != kp {
		if s, ok := kpclientdStart(o.WantClient); ok {
			p.Steps = append(p.Steps, s)
		}
	}
	return p, nil
}

var debControl = func(path string) (string, string) {
	out, err := exec.Command("dpkg-deb", "-f", path, "Package", "Version").Output()
	if err != nil {
		return "", ""
	}
	f := map[string]string{}
	for _, ln := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(ln, ":"); ok {
			f[k] = strings.TrimSpace(v)
		}
	}
	return f["Package"], f["Version"]
}

var installedVersion = func(name string) string {
	out, err := exec.Command("dpkg-query", "-W", "-f", "${db:Status-Abbrev}${Version}", name).Output()
	if err != nil {
		return ""
	}
	v, ok := strings.CutPrefix(string(out), "ii ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}

func notInstalled(debs []string) []string {
	var out []string
	for _, d := range debs {
		name, ver := debControl(d)
		if name == "" || ver == "" || installedVersion(name) != ver {
			out = append(out, d)
		}
	}
	return out
}

func kpclientdStart(want bool) (Step, bool) {
	if !want {
		return Step{}, false
	}
	return Step{
		Kind: StepRunIgnore,
		Argv: []string{"sh", "-c",
			"systemctl cat kpclientd.service >/dev/null 2>&1 || exit 0; systemctl enable --now --quiet kpclientd.service"},
		Log: "enable and start kpclientd: kno.toml wants it",
	}, true
}

func wantClient(e track.Env) bool {
	label, _, _ := config.LocalHost(e.Config)
	return e.Config.RunsKpclientd(label)
}

func kpclientdStep(want bool) Step {
	if want {
		return Step{
			Kind: StepRunIgnore,
			Argv: []string{"sh", "-c",
				"systemctl cat kpclientd.service >/dev/null 2>&1 || exit 0; " +
					"systemctl enable --quiet kpclientd.service; systemctl restart kpclientd.service"},
			Log: "enable and restart kpclientd: kno.toml wants it, and the katzenpost upgrade stops it",
		}
	}
	return Step{
		Kind: StepRunIgnore,
		Argv: []string{"sh", "-c",
			"systemctl is-enabled --quiet kpclientd.service && systemctl restart kpclientd.service"},
		Log: "restart kpclientd: the katzenpost upgrade stops it and does not start it",
	}
}

func installStep(o Options, debs []string) Step {

	return Step{
		Kind: StepRun,
		Argv: append([]string{
			"env", "DEBIAN_FRONTEND=noninteractive",
			"dpkg", "-i", "--force-confdef", "--force-confold", "--force-confmiss",
		}, debs...),
		Log: "install " + o.Tag,
	}
}

func ezjailPlan(o Options, backend string) (Plan, error) {
	pkgs := filepath.Join(o.Rel, "pkgs")
	kp, err := freebsd.OnePkg(pkgs, "katzenpost")
	if err != nil {
		return Plan{}, err
	}
	if err := refusePackagedSecret(kp); err != nil {
		return Plan{}, err
	}
	specs, err := freebsd.Specs(o.ClientJail, o.Services, func(svc string) (service.Service, error) {
		return readManifest(filepath.Join(o.Rel, "manifest", svc+".conf"))
	})
	if err != nil {
		return Plan{}, err
	}
	p := Plan{Backend: backend, Tag: o.Tag, Needs: "pkg", Packages: []string{kp}}
	enabled := enabledJails()
	var cutover strings.Builder
	for _, sp := range specs {
		add := []string{kp}
		base := strings.TrimSuffix(filepath.Base(kp), ".pkg")
		if sp.Config != "" {
			cfg, err := freebsd.OnePkg(pkgs, sp.Config)
			if err != nil {
				return Plan{}, err
			}
			if err := refusePackagedSecret(cfg); err != nil {
				return Plan{}, err
			}
			p.Packages = append(p.Packages, cfg)
			add = append(add, cfg)
			base = strings.TrimSuffix(filepath.Base(cfg), ".pkg") + "_" + base
		}
		name := sp.Jail.Name
		root := freebsd.JailRoot(name, base)
		if !rootExists(root) {
			p.Steps = append(p.Steps,
				Step{Kind: StepRunIgnore, Argv: []string{"rm", "-rf", root + ".new"}},
				Step{Kind: StepRun, Argv: []string{"mkdir", "-p", root + ".new"}},
				Step{Kind: StepRun, Log: "install " + o.Tag + " for the jail " + name + " in " + root,
					Argv: freebsd.PkgAddInRoot(root+".new", add...)},
				Step{Kind: StepRun, Argv: []string{"mv", root + ".new", root}})
		}
		p.Steps = append(p.Steps,
			Step{Kind: StepRun, Argv: []string{"ln", "-sfh", base, freebsd.CurrentRoot(name)}},
			Step{Kind: StepWrite, Path: sp.Jail.ConfPath(), Content: sp.Jail.Conf(), Mode: 0o644})
		switch {
		case !slices.Contains(enabled, name):
			p.Steps[len(p.Steps)-1].Log = "the jail " + name + " is ready with " + base + "; kno-host-cutover starts it"
		case linkTarget(freebsd.RunningRoot(name)) != base || !jailRuns(name):
			p.Steps = append(p.Steps,
				Step{Kind: StepRun, Log: "restart the jail " + name + " with " + base, Argv: []string{"service", "jail", "restart", name}},
				Step{Kind: StepRun, Argv: []string{"jls", "-j", name, "name"}},
				Step{Kind: StepRun, Argv: []string{"ln", "-sfh", base, freebsd.RunningRoot(name)}})
		}
		cutover.WriteString(cutoverLine(sp, o.Rel) + "\n")
	}
	p.Steps = append(p.Steps, Step{Kind: StepWrite, Path: freebsd.CutoverFile, Content: cutover.String(), Mode: 0o644})
	return p, nil
}

var spoolDB = regexp.MustCompile(`(?m)^\s*SpoolDB\s*=\s*"([^"]+)"`)

func cutoverLine(sp freebsd.JailSpec, rel string) string {
	if sp.Old == "" || sp.Jail.StateDir == "" {
		return sp.Jail.Name + " - -"
	}
	files := []string{"*.pem"}
	if b, err := os.ReadFile(filepath.Join(rel, "configs", filepath.Base(sp.ConfigSrc))); err == nil {
		if m := spoolDB.FindSubmatch(b); m != nil && filepath.Dir(string(m[1])) == sp.Jail.StateDir {
			files = append(files, filepath.Base(string(m[1])))
		}
	}
	return strings.Join(append([]string{sp.Jail.Name, sp.Old, sp.Jail.StateDir}, files...), " ")
}

var enabledJails = freebsd.EnabledJails

var rootExists = func(dir string) bool {
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir()
}

var linkTarget = func(p string) string {
	t, _ := os.Readlink(p)
	return t
}

var jailRuns = func(name string) bool { return exec.Command("jls", "-j", name, "name").Run() == nil }

func refusePackagedSecret(pkg string) error {
	found, err := freebsd.SecretKeyIn(pkg)
	if err != nil {
		return fmt.Errorf("refusing to install %s: %v", filepath.Base(pkg), err)
	}
	if found != "" {
		return fmt.Errorf("refusing to install %s: it ships a private key (%s);"+
			" kno never deploys or overwrites secret keys", pkg, found)
	}
	return nil
}

func clientJail(e track.Env) string {
	if runtime.GOOS != "freebsd" {
		return ""
	}
	return config.LocalClientJail(e.Config)
}

func dockerPlan(o Options, backend string) (Plan, error) {
	ver := DebVersion(o.Tag)

	engine, err := compile.FindEngine()
	if err != nil {
		return Plan{}, fmt.Errorf("docker backend: %w", err)
	}
	p := Plan{Backend: backend, Tag: o.Tag, Needs: engine}
	var compose []composeService
	for _, svc := range o.Services {
		s, err := readManifest(filepath.Join(o.Rel, "manifest", svc+".conf"))
		if err != nil {
			return Plan{}, fmt.Errorf("docker activate failed for %s: %v", svc, err)
		}
		for _, need := range []struct{ val, name string }{
			{s.DockerDir, "DOCKER_DIR"},
			{s.DockerImage, "DOCKER_IMAGE"},
			{s.DockerCont, "DOCKER_CONTAINER"},
		} {
			if need.val == "" {
				return Plan{}, fmt.Errorf("docker service %s needs %s", svc, need.name)
			}
		}

		if !filepath.IsAbs(s.DockerDir) {
			return Plan{}, fmt.Errorf(
				"docker service %s has a relative DOCKER_DIR (%s); it is a bind-mount source and"+
					" must be absolute, or the engine mounts an empty named volume over it",
				svc, s.DockerDir)
		}

		cfg := filepath.Join(o.Rel, "configs", filepath.Base(s.ConfigSrc))
		if err := release.VerifyStaged(os.Stderr, o.Rel, []string{cfg}); err != nil {
			return Plan{}, err
		}
		p.Steps = append(p.Steps, Step{Kind: StepRun, Argv: []string{"cp",
			cfg, filepath.Join(s.DockerDir, "conf", filepath.Base(s.ConfigDst))}})

		if o.Install == config.InstallCompose {
			if err := composeGuard(svc, map[string]string{
				"DOCKER_IMAGE":     s.DockerImage,
				"DOCKER_CONTAINER": s.DockerCont,
				"DOCKER_DIR":       s.DockerDir,
				"DOCKER_PORTS":     s.DockerPorts,
			}); err != nil {
				return Plan{}, err
			}
			compose = append(compose, composeService{
				Name:      svc,
				Container: s.DockerCont,
				Image:     s.DockerImage + ":" + ver,
				Ports:     ComposePorts(s.DockerPorts),
				Dir:       composeDirOf(s.DockerDir),
			})
			continue
		}

		run := dockerRunArgv(o, engine, s.DockerCont, s.DockerImage, ver,
			s.DockerDir, s.DockerPorts)
		p.Steps = append(p.Steps,
			Step{Kind: StepRunIgnore, Argv: []string{engine, "stop", s.DockerCont}},
			Step{Kind: StepRunIgnore, Argv: []string{engine, "rm", s.DockerCont}},
			Step{Kind: StepRun, Argv: run})
	}

	if len(compose) > 0 {

		p.Steps = append(p.Steps,
			Step{Kind: StepWrite, Path: ComposePath, Content: Compose(o.Tag, compose), Mode: 0o644,
				Log: "write " + ComposePath},
			Step{Kind: StepRun, Log: "reconcile the containers with " + ComposePath,
				Argv: []string{engine, "compose", "-f", ComposePath, "up", "-d", "--remove-orphans"}})
	}
	return p, nil
}

func Apply(ctx context.Context, p Plan, o Options, w io.Writer) error {
	l := track.Logger{Tool: "activate", W: w}

	if p.Needs != "" && !o.DryRun {
		if _, err := exec.LookPath(p.Needs); err != nil {
			return fmt.Errorf("%s not found", p.Needs)
		}
	}

	for _, s := range p.Steps {
		if s.Log != "" {
			l.Logf("%s", s.Log)
		}
		switch s.Kind {
		case StepRun, StepRunIgnore:
			if o.DryRun {
				fmt.Fprintf(w, "DRYRUN: %s\n", strings.Join(s.Argv, " "))
				continue
			}
			if err := runStep(ctx, s, w); err != nil && s.Kind == StepRun {
				return fmt.Errorf("%s: %v", s.Argv[0], err)
			}
		case StepWrite:
			if o.DryRun {
				fmt.Fprintf(w, "DRYRUN: write %s (%d bytes)\n", s.Path, len(s.Content))
				continue
			}
			if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(s.Path, []byte(s.Content), s.Mode); err != nil {
				return err
			}
		case StepScanSecretKeys:
			if err := scanSecretKeys(ctx, s); err != nil {
				return err
			}
		}
	}
	l.Logf("activated %s (%s)", p.Tag, p.Backend)
	return nil
}

func runStep(ctx context.Context, s Step, w io.Writer) error {
	cmd := exec.CommandContext(ctx, s.Argv[0], s.Argv[1:]...)
	cmd.Dir = s.Dir
	cmd.Stdout, cmd.Stderr = w, w
	return cmd.Run()
}

func scanSecretKeys(ctx context.Context, s Step) error {
	pkg := filepath.Base(last(s.Argv))
	out, err := exec.CommandContext(ctx, s.Argv[0], s.Argv[1:]...).Output()
	if err != nil {
		return fmt.Errorf("refusing to install %s: cannot list its contents to"+
			" check for private keys (%v)", pkg, err)
	}
	found, err := tarSecretKey(out)
	if err != nil {

		return fmt.Errorf("refusing to install %s: cannot read its contents (%v)", pkg, err)
	}
	if found != "" {
		return fmt.Errorf("refusing to install %s: it ships a private key (%s);"+
			" kno never deploys or overwrites secret keys", pkg, found)
	}
	return nil
}

func tarSecretKey(stream []byte) (string, error) {
	tr := tar.NewReader(bytes.NewReader(stream))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		if fsutil.IsSecretKey(path.Base(h.Name)) {
			return h.Name, nil
		}
	}
}

func ReleaseBackend(rel string) string {
	m, _ := filepath.Glob(filepath.Join(rel, "manifest", "*.conf"))
	if len(m) > 0 {
		if s, err := readManifest(m[0]); err == nil && s.Backend != "" {
			return s.Backend
		}
	}

	return "systemd"
}

func DebVersion(tag string) string { return strings.TrimPrefix(tag, "v") }

func readManifest(path string) (service.Service, error) {
	f, err := os.Open(path)
	if err != nil {
		return service.Service{}, err
	}
	defer f.Close()
	m := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		m[strings.TrimSpace(k)] = strings.Trim(v, `"`)
	}
	if err := sc.Err(); err != nil {
		return service.Service{}, err
	}
	s := service.Service{
		Name:         strings.TrimSuffix(filepath.Base(path), ".conf"),
		Backend:      m["BACKEND"],
		Unit:         m["UNIT"],
		Build:        m["BUILD"],
		BinarySrc:    m["BINARY_SRC"],
		BinaryDst:    m["BINARY_DST"],
		ConfigSrc:    m["CONFIG_SRC"],
		ConfigDst:    m["CONFIG_DST"],
		StateDir:     m["STATE_DIR"],
		SecretKeys:   m["SECRET_KEYS"],
		Jail:         m["JAIL"],
		PrestartHook: m["PRESTART_HOOK"],
		ServiceUser:  m["SERVICE_USER"],
		DockerDir:    m["DOCKER_DIR"],
		DockerImage:  m["DOCKER_IMAGE"],
		DockerCont:   m["DOCKER_CONTAINER"],
		DockerPorts:  m["DOCKER_PORTS"],
	}
	s.KeysSrc = strings.Fields(m["KEYS_SRC"])
	s.AuxFiles = strings.Fields(m["AUX_FILES"])
	return s, nil
}

func last(argv []string) string { return argv[len(argv)-1] }

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func readable(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func Main() { os.Exit(Run(os.Stderr, os.Args[1:])) }

func Run(stderr io.Writer, args []string) int {
	l := track.Logger{Tool: "activate", W: stderr}
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stderr, "kno activate: install a built release on this host and start its services")
		fmt.Fprintln(stderr, "usage: kno activate <release-dir> <tag>")
		fmt.Fprintln(stderr, "  DRY_RUN=1 prints the commands instead of running them.")
		return 0
	}
	if len(args) < 2 {
		fmt.Fprintln(stderr, "usage: kno activate <release-dir> <tag>")
		return 1
	}
	if os.Getenv("KNO_ATTEST_VERIFY") == "1" {
		l.Errorf("KNO_ATTEST_VERIFY=1 asks for an attestation check of each package, and kno" +
			" has no such check. kno installs nothing. Unset KNO_ATTEST_VERIFY")
		return 1
	}
	e, err := track.LoadEnv()
	if err != nil {
		fmt.Fprintf(stderr, "kno: config error (%v); fix your kno.toml or unset it\n", err)
		return 1
	}
	install, err := installFor(e)
	if err != nil {
		fmt.Fprintf(stderr, "kno: %v\n", err)
		return 1
	}
	o := Options{
		Rel:        args[0],
		Tag:        args[1],
		Threshold:  e.Config.SignatureThreshold(),
		Services:   e.Services,
		Arch:       HostArch(),
		OS:         HostOS(),
		Repo:       e.Repo,
		Network:    e.Network,
		Install:    install,
		ClientJail: clientJail(e),
		WantClient: wantClient(e),

		DryRun: cli.DryRun() || os.Getenv("DRY_RUN") == "1",
	}

	if rc := checkRelease(l, o); rc != 0 {
		return rc
	}

	p, err := PlanFor(o)
	if err != nil {
		l.Errorf("%v", err)
		return 1
	}

	if len(p.Packages) > 0 && sumsPresent(o) {
		if rc := verifyPackageDigests(l, o, p.Packages); rc != 0 {
			return rc
		}
	}
	if err := Apply(context.Background(), p, o, stderr); err != nil {
		l.Errorf("%v", err)
		return 1
	}
	return 0
}

func checkRelease(l track.Logger, o Options) int {
	sums := filepath.Join(o.Rel, "pkgs", build.SumsName)
	allow := o.allowlist()

	if _, err := os.Stat(sums); err != nil {

		if backend := hostBackend(o); backend == "" || backend == "systemd" || backend == "freebsd-ezjail" {
			l.Errorf("the release staged no %s, so nothing describes the"+
				" packages", build.SumsName)
			l.Logf("  Refusing to install %s: a release that cannot be checked", o.Tag)
			l.Logf("  is not a release that passed a check. Push again from a kno")
			l.Logf("  that ships the digest list, or build here with 'kno update'.")
			return 1
		}
		return 0
	}
	if _, err := os.Stat(allow); err != nil {
		return 0
	}
	if !sign.Available() {
		l.Warnf("%s names signers but there is no ssh-keygen to check them with",
			allow)
		l.Logf("  Installing on the digest list alone.")
		return 0
	}
	who, errs := validSigners(sums, allow)
	if len(errs) > 0 {
		for _, e := range errs {
			l.Errorf("%v", e)
		}
		l.Errorf("%s carries a signature this host rejects", o.Tag)
		l.Logf("  kno installs nothing. 'kno release verify --dir %s' repeats this check.", filepath.Join(o.Rel, "pkgs"))
		return 1
	}
	k := o.threshold()
	if len(who) < k {
		l.Warnf("%s has %d of %d signatures. kno installs it by its digest list.", o.Tag, len(who), k)
		return 0
	}
	l.Logf("release %s signed by %s (%d of %d)",
		o.Tag, strings.Join(who, ", "), len(who), k)
	return 0
}

func hostBackend(o Options) string {
	if o.Backend != "" {
		return o.Backend
	}
	repo, err := config.EmbeddedRepo()
	if err != nil {
		return ""
	}
	for _, svc := range o.Services {
		s, err := service.ResolveService(repo, o.Network, svc)
		if err != nil || s.Backend == "" {
			continue
		}
		return s.Backend
	}
	return ""
}

func (o Options) threshold() int {
	if o.Threshold > 0 {
		return o.Threshold
	}
	return config.DefaultThreshold
}

func validSigners(sums, allow string) (who []string, errs []error) {
	cands := []string{sums + ".sig"}
	if more, err := filepath.Glob(filepath.Join(filepath.Dir(sums), "sigs", "*.sig")); err == nil {
		cands = append(cands, more...)
	}
	seen := map[string]bool{}
	for _, sig := range cands {
		if _, err := os.Stat(sig); err != nil {
			continue
		}
		res, err := sign.Verify(sums, sig, allow)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if res.Principal != "" && !seen[res.Principal] {
			seen[res.Principal] = true
			who = append(who, res.Principal)
		}
	}
	sort.Strings(who)
	return who, errs
}

func HostArch() string { return compile.HostDebArch() }

func HostOS() string {
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	var id, ver string
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"`)
		switch k {
		case "ID":
			id = v
		case "VERSION_ID":
			ver = v
		}
	}
	if id == "" || ver == "" {
		return ""
	}
	return id + "-" + ver
}

func configPkg(pkgs, svc, ver, arch string) (string, error) {
	for _, a := range []string{arch, deb.ArchAll} {
		if a == "" {
			continue
		}
		found, err := filepath.Glob(filepath.Join(pkgs, configPkgPrefix+svc+"_*_"+a+".deb"))
		if err != nil {
			return "", err
		}
		var readableOnes []string
		for _, c := range found {
			if readable(c) {
				readableOnes = append(readableOnes, c)
			}
		}
		if len(readableOnes) > 0 {
			return deb.Newest(readableOnes), nil
		}
	}
	return "", fmt.Errorf("missing config package for %s: no %s%s_%s_{%s,%s}.deb in %s",
		svc, configPkgPrefix, svc, ver, arch, deb.ArchAll, pkgs)
}

func orUnknown(s string) string {
	if s == "" {
		return "an unknown distribution"
	}
	return s
}

func installFor(e track.Env) (string, error) {
	raw := e.Config.InstallMethod("")
	if label, _, ok := config.LocalHost(e.Config); ok {
		raw = e.Config.InstallMethod(label)
	}

	m, ok := config.NormalizeInstall(raw)
	if !ok {
		return "", fmt.Errorf("install = %q in this host's kno.toml is not an install method"+
			" (want one of %s); fix it there rather than here",
			raw, strings.Join(config.Installs, ", "))
	}
	return m, nil
}

func verifyPackageDigests(l track.Logger, o Options, debs []string) int {
	pkgs := filepath.Join(o.Rel, "pkgs")
	entries, err := build.SumsEntries(filepath.Join(pkgs, build.SumsName))
	if err != nil {
		l.Errorf("cannot read %s: %v", build.SumsName, err)
		return 1
	}
	want := make(map[string]string, len(entries))
	for _, e := range entries {
		want[e.Name] = e.Sha256
	}
	bad := 0
	for _, d := range debs {
		name := filepath.Base(d)
		sum, ok := want[name]
		if !ok {
			l.Errorf("%s is not named in %s", name, build.SumsName)
			bad++
			continue
		}
		got, _, err := fsutil.SHA256File(d)
		if err != nil {
			l.Errorf("cannot hash %s: %v", name, err)
			bad++
			continue
		}
		if got != sum {
			l.Errorf("%s does not match the digest %s records", name, build.SumsName)
			l.Logf("  recorded %s", sum)
			l.Logf("  on disk %s", got)
			bad++
		}
	}
	if bad > 0 {
		l.Logf("  Refusing to install %s: %d package(s) are not what the list names.", o.Tag, bad)
		return 1
	}
	return 0
}

func sumsPresent(o Options) bool {
	_, err := os.Stat(filepath.Join(o.Rel, "pkgs", build.SumsName))
	return err == nil
}

func dockerRunArgv(o Options, engine, container, image, ver, dir, ports string) []string {
	run := []string{engine, "run", "-d", "--restart=unless-stopped",
		"--pull=never", "--name", container, "-h", container}
	run = append(run, strings.Fields(ports)...)
	return append(run,
		"-v", dir+"/conf:/conf",
		"-v", dir+"/data:/var/lib/katzenpost",
		"-v", dir+"/logs:/var/log/katzenpost",
		image+":"+ver)
}
