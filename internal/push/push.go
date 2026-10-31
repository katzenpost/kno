package push

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/katzenpost/kno/internal/argcheck"
	"github.com/katzenpost/kno/internal/build"
	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/layout"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/service"
	"github.com/katzenpost/kno/internal/sign"
	"github.com/katzenpost/kno/internal/sshx"
	"github.com/katzenpost/kno/internal/track"
)

const (
	ModeCopy    = "copy"
	ModeInstall = "install"
	ModeFull    = "full"
	ModeKno     = "kno"
)

const configPkgPrefix = knopath.ConfigPkgPrefix

type Options struct {
	Repo, Network string

	Label, Node string

	Tag, Rel string

	Mode      string
	Privilege string

	Services []string

	Arch, OS string

	SSH, SCP, RSYNC string

	RemoteRsync bool

	KnoVersion string

	DryRun bool

	Progress bool
}

func (o Options) asRoot(cmd string) string { return sshx.AsRootWith(o.Privilege, cmd) }

func (o Options) rootSSH(cmd string) []string {
	return sshx.SSHForRoot(o.SSH, o.Node, o.Privilege, cmd)
}

type Step struct {
	Argv       []string
	Env        []string
	Note       string
	BestEffort bool
}

type Plan struct {
	Node, Tag, Mode string
	Packages        []string
	Steps           []Step
}

func NodeOf(svc string) string {
	if i := strings.Index(svc, "-pq-"); i > 0 {
		return svc[:i]
	}
	return svc
}

var probeTimeout = 3 * time.Second

type limitWriter struct {
	w io.Writer
	n int
}

func (l *limitWriter) Write(b []byte) (int, error) {
	if l.n <= 0 {
		return len(b), nil
	}
	k := min(len(b), l.n)
	l.n -= k
	if _, err := l.w.Write(b[:k]); err != nil {
		return 0, err
	}
	return len(b), nil
}

func ProbeArgv(ssh, node string) []string {
	return sshx.SSH(ssh, node, probeScript)
}

const probeScript = sshx.HostIdent + `; r=no; command -v rsync >/dev/null 2>&1 && r=yes; printf " %s\n" "$r"`

func ParseProbe(out string) (arch, osName string, rsync bool, answered bool) {
	line := out
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	f := strings.Fields(line)
	if len(f) > 0 {
		arch = sshx.IdentValue(f[0])
	}
	if len(f) > 1 {
		osName = f[1]
	}
	if len(f) > 2 {
		rsync, answered = f[2] == "yes", true
	}
	return arch, osName, rsync, answered
}

func Probe(ctx context.Context, o Options, warn func(format string, a ...interface{})) Options {
	if o.DryRun {
		return o
	}
	if _, err := exec.LookPath(o.SSH); err != nil {
		return o
	}
	argv := ProbeArgv(o.SSH, o.Node)
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.WaitDelay = time.Second
	var buf bytes.Buffer
	cmd.Stdout = &limitWriter{w: &buf, n: 64 << 10}
	if err := cmd.Run(); err != nil {
		return o
	}
	out := buf.Bytes()
	arch, osName, rsync, answered := ParseProbe(string(out))
	if !isFreeBSD(o) {
		o = applyProbe(o, arch, osName, warn)
	}
	if answered && !rsync {

		o.RemoteRsync = false
	}
	return o
}

func applyProbe(o Options, arch, osName string, warn func(format string, a ...interface{})) Options {
	if _, _, err := compile.ArchToGo(arch); arch != "" && err != nil {
		warn("%s reports the arch %q, which kno does not know. kno uses machines/%s.toml", o.Node, cli.Clean(arch), o.Label)
		arch = ""
	}
	if osName != "" && osName != "-" && !compile.IsOSToken(compile.OSToken(osName)) {
		warn("%s reports the OS %q, which kno does not know. kno uses machines/%s.toml", o.Node, cli.Clean(osName), o.Label)
		osName = ""
	}
	if arch != "" && arch != o.Arch {
		if o.Arch != "" {
			warn("machines/%s.toml says %s is %s but %s reports %s. kno uses %s", o.Label, o.Label, o.Arch, o.Node, arch, arch)
		}
		o.Arch = arch
	}
	if osName != "" && osName != "-" && osName != o.OS {
		if o.OS != "" {
			warn("machines/%s.toml says %s runs %s but %s reports %s. kno uses %s", o.Label, o.Label, o.OS, o.Node, osName, osName)
		}
		o.OS = osName
	}
	return o
}

func NeedsBuild(rel string) bool {
	fi, err := os.Stat(filepath.Join(rel, "manifest"))
	return err != nil || !fi.IsDir()
}

func releaseDir(tag string) string { return filepath.Dir(knopath.Packages(tag)) }

func BuildStep(o Options) Step {
	argv := []string{knopath.Running(), "build", "--tag", o.Tag, "--host", o.Label}
	if o.Network != "" {
		argv = append(argv, "--network", o.Network)
	}
	if o.Arch != "" {
		argv = append(argv, "--arches", o.Arch)
	}
	return Step{
		Argv: argv,
		Env:  buildEnv(o),
	}
}

func buildEnv(o Options) []string {
	env := []string{
		"KNO_HOST=" + o.Label,
		"KNO_TARGET_ARCH=" + o.Arch,
		"KNO_TARGET_OS=" + o.OS,
	}
	if !isFreeBSD(o) {
		env = append(env, "KNO_SKIP_FREEBSD=1")
	}
	return append(env, "KNO_SKIP_DOCKER=1")
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func configArch(o Options) string {
	if o.Arch == "" {
		return deb.ArchAll
	}
	return o.Arch
}

func PlanFor(o Options) (Plan, error) {
	if len(o.Services) == 0 {
		return Plan{}, fmt.Errorf("host %q has no services placed on it. Nothing to push", o.Label)
	}
	if err := checkRemote(o); err != nil {
		return Plan{}, err
	}
	if err := requireSystemd(o); err != nil {
		return Plan{}, err
	}
	p := Plan{Node: o.Node, Tag: o.Tag, Mode: o.Mode}
	switch o.Mode {
	case ModeCopy, ModeInstall, ModeFull:
		if isFreeBSD(o) {
			return planInstallFreeBSD(o, p)
		}
		return planInstall(o, p)
	case ModeKno:
		if isFreeBSD(o) {
			return Plan{}, fmt.Errorf("host %q runs the freebsd-ezjail backend. It updates its own kno"+
				" with kno update on the host", o.Label)
		}
		return planKno(o, p)
	}
	return Plan{}, fmt.Errorf("unknown push mode '%s' (copy|install|full|kno)", o.Mode)
}

func checkRemote(o Options) error {
	if err := argcheck.Target("ssh target", o.Node); err != nil {
		return err
	}
	if err := argcheck.Arg("tag", o.Tag); err != nil {
		return err
	}
	for _, svc := range o.Services {
		s, err := resolve(o, svc)
		if err != nil {
			return err
		}
		unit := layout.Unit(s, o.Tag)
		if unit == "" {
			unit = svc
		}
		if err := argcheck.Arg("unit for "+svc, unit); err != nil {
			return err
		}
		for _, p := range []struct{ what, val string }{
			{"BINARY_DST for " + svc, s.BinaryDst},
			{"CONFIG_DST for " + svc, s.ConfigDst},
		} {
			if p.val == "" {
				continue
			}
			if err := argcheck.Path(p.what, p.val); err != nil {
				return err
			}
		}
	}
	return nil
}

func backendOf(o Options) (string, error) {
	backend := ""
	for _, svc := range o.Services {
		s, err := resolve(o, svc)
		if err != nil {
			return "", err
		}
		b := s.Backend
		if b == "" {
			b = "systemd"
		}
		if backend != "" && b != backend {
			return "", fmt.Errorf("host %q mixes the %s and %s backends. kno push installs one", o.Label, backend, b)
		}
		backend = b
	}
	return backend, nil
}

func isFreeBSD(o Options) bool {
	b, err := backendOf(o)
	return err == nil && b == "freebsd-ezjail"
}

func requireSystemd(o Options) error {
	if isFreeBSD(o) {
		return nil
	}
	for _, svc := range o.Services {
		s, err := resolve(o, svc)
		if err != nil {
			return err
		}
		if s.Backend != "" && s.Backend != "systemd" {
			return fmt.Errorf("host %q uses the %s backend (%s). kno push supports systemd"+
				" and FreeBSD ezjail hosts. On %s, run kno build, then kno activate",
				o.Label, s.Backend, svc, o.Label)
		}
	}
	return nil
}

func planInstall(o Options, p Plan) (Plan, error) {
	pkgs := filepath.Join(o.Rel, "pkgs")

	binArch := o.Arch
	if binArch == "" {
		binArch = "*"
	}

	bins, err := filepath.Glob(filepath.Join(pkgs,
		"katzenpost_"+deb.OSVersion(o.Tag, "", o.OS)+"_"+binArch+".deb"))
	if err != nil {
		return Plan{}, err
	}
	if len(bins) == 0 && o.OS != "" {

		if other, _ := filepath.Glob(filepath.Join(pkgs, "katzenpost_*_"+binArch+".deb")); len(other) > 0 {
			return Plan{}, fmt.Errorf("no katzenpost built for %s on %s. The release"+
				" has %s (build it: kno build --host %s)",
				binArch, o.OS, strings.Join(baseNames(other), " "), o.Label)
		}
	}
	p.Packages = append(p.Packages, bins...)

	for _, svc := range o.Services {
		pat := filepath.Join(pkgs, configPkgPrefix+svc+"_*_"+configArch(o)+".deb")
		found, err := filepath.Glob(pat)
		if err != nil {
			return Plan{}, err
		}
		if len(found) == 0 {
			return Plan{}, fmt.Errorf(
				"no config package for %s (no file %s). Run: kno build --host %s",
				svc, pat, o.Label)
		}
		p.Packages = append(p.Packages, newestOnly(found))
	}

	if len(p.Packages) == 0 {
		return Plan{}, fmt.Errorf("the build has no packages for %s", o.Label)
	}

	kno, err := knoPackages(o, pkgs)
	if err != nil {
		return Plan{}, err
	}
	p.Packages = append(p.Packages, kno...)

	remote := RemoteRelease(o.Tag)
	p.Steps = append(p.Steps,

		Step{Argv: sshx.SSH(o.SSH, o.Node, "mkdir -p "+remote+"/pkgs")},
	)

	p.Steps = append(p.Steps, transfer(o, pkgs, append(slices.Clone(p.Packages), sumsFiles(pkgs)...),
		remote+"/pkgs", false)...)
	if o.Mode == ModeCopy {
		return p, nil
	}
	p.Steps = append(p.Steps,

		Step{Argv: o.rootSSH(bootstrapCmdWith(remote, bootstrapNames(p.Packages), o.Privilege)),
			Note: "installed kno on " + o.Node},
	)
	var units []string
	if o.Mode == ModeFull {
		for _, svc := range o.Services {
			s, err := resolve(o, svc)
			if err != nil {
				return Plan{}, err
			}
			unit := layout.Unit(s, o.Tag)
			if unit == "" {
				unit = svc
			}
			units = append(units, unit)
		}
	}
	activate := "kno activate "
	if len(units) > 0 && !layout.Legacy(o.Tag) {
		activate += "--restart=" + strings.Join(units, ",") + " "
	}
	p.Steps = append(p.Steps,
		Step{Argv: o.rootSSH(o.asRoot(activate + remote + " " + o.Tag))},
	)
	if o.Mode != ModeFull || !layout.Legacy(o.Tag) {
		return p, nil
	}
	for _, unit := range units {
		p.Steps = append(p.Steps, Step{

			Argv: o.rootSSH(
				o.asRoot("systemctl reset-failed "+unit) + " || true; " + o.asRoot("systemctl restart "+unit)),
			BestEffort: true,
		})
	}
	if layout.Legacy(o.Tag) {
		p.Steps = append(p.Steps, Step{
			Argv: o.rootSSH(
				"if systemctl is-enabled --quiet kpclientd.service; then " +
					o.asRoot("systemctl restart kpclientd.service") + "; fi"),
			Note:       "kpclientd restarted: the kno of this release does not restart it",
			BestEffort: true,
		})
	}
	return p, nil
}

func knoPackages(o Options, pkgs string) ([]string, error) {
	var out []string
	arch := o.Arch
	if arch == "" {
		arch = "*"
	}
	tools, err := filepath.Glob(filepath.Join(pkgs, "kno_*_"+arch+".deb"))
	if err != nil {
		return nil, err
	}
	if len(tools) == 0 {
		return nil, fmt.Errorf("no kno package for %s in %s. The far side needs"+
			" kno to install a release (build it: kno build --host %s)",
			arch, pkgs, o.Label)
	}

	newest := newestOnly(tools)
	if want := o.KnoVersion; want != "" {
		exact := filepath.Join(pkgs, "kno_"+want+"_"+arch+".deb")
		if !exists(exact) {
			return nil, fmt.Errorf("%s needs kno %s for %s. The newest built kno is %s."+
				" kno does not install an older kno, because %s would then never"+
				" get the new kno. Run: kno build --host %s",
				o.Tag, want, arch, filepath.Base(newest), o.Label, o.Label)
		}
		newest = exact
	}
	out = append(out, newest)

	for _, pat := range []string{
		"kno-host-config-" + o.Label + "_*_" + configArch(o) + ".deb",
		"kno-host-config_*_all.deb",
	} {
		hc, err := filepath.Glob(filepath.Join(pkgs, pat))
		if err != nil {
			return nil, err
		}
		if len(hc) > 0 {
			out = append(out, newestOnly(hc))
			break
		}
	}

	return out, nil
}

func planKno(o Options, p Plan) (Plan, error) {
	pkgs := filepath.Join(o.Rel, "pkgs")
	kno, err := knoPackages(o, pkgs)
	if err != nil {
		return Plan{}, err
	}
	p.Packages = kno
	remote := RemoteRelease(o.Tag)
	p.Steps = append(p.Steps, Step{Argv: sshx.SSH(o.SSH, o.Node, "mkdir -p "+remote+"/pkgs")})
	p.Steps = append(p.Steps, transfer(o, pkgs, append(slices.Clone(p.Packages), sumsFiles(pkgs)...),
		remote+"/pkgs", false)...)
	p.Steps = append(p.Steps, Step{
		Argv: o.rootSSH(bootstrapCmdWith(remote, bootstrapNames(p.Packages), o.Privilege)),
		Note: "installed kno on " + o.Node + ". katzenpost and its services did not change.",
	})
	return p, nil
}

func RemoteRelease(tag string) string {
	return ".local/share/katzenpost/kno/pkgs/" + tag
}

func transfer(o Options, srcDir string, files []string, dst string, prune bool) []Step {
	if len(files) == 0 {
		return nil
	}
	if !o.RemoteRsync {
		scp := append(append([]string{o.SCP}, sshx.Args()...), files...)
		steps := []Step{{Argv: append(scp, o.Node+":"+dst+"/")}}
		if prune {
			clear := "rm -f " + dst + "/*.deb " + dst + "/*.pkg " + dst + "/*.conf"
			return append([]Step{{Argv: sshx.SSH(o.SSH, o.Node, clear)}}, steps...)
		}
		return steps
	}

	argv := []string{o.RSYNC, "-av", "--partial"}
	if o.Progress {
		argv = append(argv, "--progress")
	}
	if prune {
		argv = append(argv, "--delete", "--delete-excluded")
	}
	argv = append(argv, sshx.RsyncArgs(o.SSH)...)
	for _, f := range files {
		argv = append(argv, "--include="+filepath.Base(f))
	}

	argv = append(argv, "--exclude=*")

	return []Step{{Argv: append(argv, srcDir+string(filepath.Separator), o.Node+":"+dst+"/")}}
}

func resolve(o Options, svc string) (service.Service, error) {
	e := track.Env{Repo: o.Repo, Network: o.Network}
	if !e.HasService(svc) {
		return service.Service{}, fmt.Errorf(
			"service '%s' is not in the network topology (configs/%s.toml missing)", svc, svc)
	}
	return service.ResolveService(o.Repo, o.Network, svc)
}

func bootstrapCmd(remote string, names []string) string {
	return bootstrapCmdWith(remote, names, "")
}

func bootstrapCmdWith(remote string, names []string, privilege string) string {
	pkgs := remote + "/pkgs"

	if len(names) == 0 {
		return "set -e; echo 'kno: no kno package to install' >&2; exit 1"
	}

	sums := build.SumsName
	verify := "[ -f " + sums + " ] || " +
		`{ echo 'kno: ` + sums + ` is not with the copied packages. kno does not install them.' >&2; exit 1; }; ` +
		"sha256sum -c --ignore-missing " + sums + " >/dev/null || " +
		`{ echo 'kno: staged packages do not match ` + sums + `' >&2; exit 1; }; ` +
		sigCheck(sums, sign.NodePathFor("linux"))
	listed := "for p in " + strings.Join(names, " ") + "; do " +
		`grep -q "^[0-9a-f]\{64\}  $p\$" ` + sums + " || " +
		`{ echo "kno: $p is not in ` + sums + `. kno does not install it" >&2; exit 1; }; done; `
	debs := strings.Join(names, " ")
	return "set -e; cd " + pkgs + "; " + verify + listed +
		"if kno activate --help 2>&1 | grep -q -- --kno; then " +
		sshx.AsRootWith(privilege, "kno activate --kno "+debs) + "; else " +
		sshx.AsRootWith(privilege, "dpkg -i "+debs) + "; fi; " +
		"command -v kno >/dev/null || " +
		`{ echo "kno: installed the package but kno is not on PATH" >&2; exit 1; }`
}

func sigCheck(sums, allow string) string {
	return "if [ -f " + sums + ".sig ] && [ -f " + allow + " ]; then " +
		"s=$(ssh-keygen -Y find-principals -s " + sums + ".sig -f " + allow +
		" 2>/dev/null | head -n1); " +
		`[ -n "$s" ] || { echo 'kno: ` + sums + `.sig is not from an allowlisted signer' >&2; exit 1; }; ` +
		"ssh-keygen -Y verify -f " + allow + ` -I "$s" -n ` + sign.Namespace +
		" -s " + sums + ".sig < " + sums + " >/dev/null; fi; "
}

func bootstrapNames(pkgs []string) []string {
	var kno, hostConfig []string
	for _, p := range pkgs {
		switch base := filepath.Base(p); {
		case strings.HasPrefix(base, "kno-host-config"):
			hostConfig = append(hostConfig, base)
		case strings.HasPrefix(base, "kno_"):
			kno = append(kno, base)
		}
	}
	return append(kno, hostConfig...)
}

func newestOnly(paths []string) string { return deb.Newest(paths) }

func baseNames(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Base(p)
	}
	return out
}

func sumsFiles(pkgs string) []string {
	var out []string
	for _, n := range []string{build.SumsName, build.SumsName + ".sig"} {
		p := filepath.Join(pkgs, n)
		if exists(p) {
			out = append(out, p)
		}
	}
	return out
}
