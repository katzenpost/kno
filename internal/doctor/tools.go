package doctor

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/katzenpost/kno/internal/knopath"
)

type Scope string

const (
	ScopeCore Scope = "core"

	ScopeBuild Scope = "build"

	ScopeNode Scope = "node"

	ScopePush Scope = "push"

	ScopeRemote Scope = "remote"

	ScopeVendored Scope = "vendored"
)

type Tool struct {
	Name string

	Env string

	Scope Scope

	For string

	Why string

	Version []string

	VersionOnStderr bool

	Package string

	AltFor string

	Install string

	GOOS string
}

func (t Tool) wanted() bool { return t.wantedOn(runtime.GOOS) }

func (t Tool) wantedOn(goos string) bool {
	if t.GOOS == "" {
		return true
	}
	for _, g := range strings.Fields(t.GOOS) {
		if g == goos {
			return true
		}
	}
	return false
}

var Tools = []Tool{
	{
		Name: "apt-get", GOOS: "linux", Package: "apt", Scope: ScopeNode,
		For:     "kno activate (install = apt)",
		Why:     "installs a release by name and version out of the local repository, which resolves dependencies and removes a conflicting package where dpkg -i only reports the conflict",
		Version: []string{"--version"},
	},
	{
		Name: "apt", GOOS: "linux", Scope: ScopeNode, For: "kno deps, kno bootstrap",
		Why:     "installs the host's build and runtime dependencies on Debian",
		Version: []string{"--version"},
	},
	{
		Name: "chown", Package: "coreutils", Scope: ScopeNode, For: "kno bootstrap",
		Why: "gives the updater user its bare repo",
	},
	{
		Name: "cp", Package: "coreutils", Scope: ScopeNode, For: "kno activate (container backend)",
		Why: "stages a config into the container's conf directory",
	},
	{
		Name: "dpkg", GOOS: "linux", Scope: ScopeNode, For: "kno activate, kno update",
		Why:     "reports the host architecture, which decides which package to install",
		Version: []string{"--version"},
	},
	{
		Name: "dpkg-deb", GOOS: "linux", Package: "dpkg", Scope: ScopeNode, For: "kno activate, kno sim",
		Why:     "lists a package's contents, which is how a package shipping a private key is refused",
		Version: []string{"--version"},
	},
	{
		Name: "env", Package: "coreutils", Scope: ScopeNode, For: "kno deps",
		Why: "sets DEBIAN_FRONTEND/ASSUME_ALWAYS_YES for one command without touching kno's own environment",
	},
	{
		Name: "jls", Scope: ScopeNode, GOOS: "freebsd",
		For: "kno activate, kno verify (freebsd-ezjail backend)",
		Why: "checks that a service jail runs after its restart, and finds the root of the jail that runs kpclientd",
	},
	{
		Name: "jexec", Scope: ScopeNode, GOOS: "freebsd",
		For: "kno verify (freebsd-ezjail backend)",
		Why: "runs kpfetch inside the kpclientd jail, where the thin-client socket is",
	},
	{
		Name: "service", Scope: ScopeNode, GOOS: "freebsd",
		For: "kno activate, kno verify (freebsd-ezjail backend)",
		Why: "restarts each service jail in the jail kno with service jail, and asks an old ezjail jail's rc.d service with service -j",
	},
	{
		Name: "genkeypair", Scope: ScopeVendored, For: "kno sim",
		Why: "generates the simulated network's keys; kno builds it from the pinned katzenpost source and runs it by path",
	},
	{
		Name: "git", Scope: ScopeCore, For: "kno track, kno build, kno status, kno update",
		Why:     "reads the pinned refs and tags that decide what is built, and fetches katzenpost",
		Version: []string{"--version"},
	},
	{
		Name: "gpg", GOOS: "linux darwin", Package: "gnupg", Scope: ScopeBuild, For: "kno repo publish",
		Why: "signs the local package repository's Release, which is what makes apt willing" +
			" to install from it; only a machine that BUILDS needs gpg, because verifying" +
			" needs gpgv, which apt already depends on",
		Version: []string{"--version"},
	},
	{
		Name: "go", Package: "golang-go", Scope: ScopeBuild, For: "kno build, kno check --repro",
		Why:     "compiles kno and the vendored toolchain outside the build container",
		Version: []string{"version"},
	},
	{
		Name: "install", Package: "coreutils", Scope: ScopeNode, For: "kno bootstrap, kno schedule",
		Why: "creates directories and unit files with an explicit mode and owner in one step",
	},
	{
		Name: "kno", Scope: ScopeCore, For: "kno push, kno setup, kno update",
		Why: "kno re-execs itself for the steps that need a child process or root; doctor checks the kno that runs now",
	},
	{
		Name: "pkg", Scope: ScopeNode, GOOS: "freebsd",
		For:     "kno activate, kno deps (FreeBSD)",
		Why:     "installs packages on FreeBSD",
		Version: []string{"--version"},
	},
	{
		Name: "podman", Env: "PODMAN", GOOS: "linux darwin", Scope: ScopeBuild,
		For:     "kno build, kno deb, kno check --repro, kno sim",
		Why:     "every reproducible build runs in the pinned container; dpkg-deb runs there too",
		Version: []string{"--version"},
	},
	{
		Name: "podman", Env: "KNO_CONTAINER_ENGINE", GOOS: "linux darwin", Scope: ScopeBuild, For: "kno docker",
		Why: "builds the per-role container images; this path reads a different override" +
			" from the build path, so a machine can be ready for one and not the other",
		Version: []string{"--version"},
	},
	{
		Name: "kpfetch", Env: "KNO_FETCH", Scope: ScopeNode, For: "kno probe, kno verify, kno update",
		Why: "gets the current consensus. kno verify uses it to check that a consensus exists and that this node is in it",
	},
	{
		Name: "rm", Package: "coreutils", Scope: ScopeNode, For: "kno schedule, kno bootstrap",
		Why: "removes the update trigger's units and cron fragment, and the tools an earlier kno left on PATH",
	},
	{
		Name: "rmdir", Package: "coreutils", Scope: ScopeNode, For: "kno bootstrap",
		Why: "removes a superseded directory only when it is empty; rm -rf would take whatever else is in it",
	},
	{
		Name: "rsync", Env: "RSYNC", Install: "rsync", Scope: ScopePush, For: "kno push",
		Why: "ships a release, sending only what the host does not already have" +
			" and clearing whatever else is in its staging directory",
		Version: []string{"--version"},
	},
	{
		Name: "scp", Package: "openssh-client", Install: "openssh-client", Env: "SCP", Scope: ScopePush, For: "kno push, kno schedule",
		Why: "ships packages and unit files to the host being deployed",
	},
	{
		Name: "sh", Package: "dash", Scope: ScopeBuild, For: "kno deb (DEB_BUILD hook)",
		Why: "runs an operator's packaging hook, and the build container's compile line",
	},
	{
		Name: "ssh", Package: "openssh-client", Install: "openssh-client", Env: "SSH", Scope: ScopePush, For: "kno push, kno schedule",
		Why:             "reaches the host being deployed; the far side runs a shell over what it is given",
		Version:         []string{"-V"},
		VersionOnStderr: true,
	},
	{
		Name: "sudo", GOOS: "linux darwin", Scope: ScopeNode, For: "kno setup, kno schedule, kno bootstrap",
		Why:     "the few steps that need root ask for it explicitly rather than requiring kno to run as root",
		Version: []string{"--version"},
	},
	{
		Name: "apt-cache", GOOS: "linux", Package: "apt", Scope: ScopeNode, For: "kno doctor",
		Why:     "reads the priority apt resolved for the local repository, which is the only place a pin that matches nothing is visible",
		Version: []string{"--version"},
	},
	{
		Name: "visudo", GOOS: "linux", Package: "sudo", Scope: ScopeNode, For: "kno bootstrap",
		Why:     "checks the generated sudoers drop-in before it replaces the installed one; an unparsable drop-in is a host nobody can sudo on",
		Version: []string{"-V"},
	},
	{
		Name: "systemctl", GOOS: "linux", Package: "systemd", Scope: ScopeNode, For: "kno status, kno verify, kno doctor, kno schedule",
		Why:     "reads and restarts the units the services run as",
		Version: []string{"--version"},
	},
	{
		Name: "tar", Scope: ScopeBuild, For: "kno build (FreeBSD)",
		Why: "extracts the FreeBSD cross sysroot from base.txz; Go's archive/tar cannot, because the tarball is xz",
	},
	{
		Name: "tee", Package: "coreutils", Scope: ScopeNode, For: "kno schedule cron",
		Why: "writes the cron fragment through sudo without a shell redirection",
	},
	{
		Name: "useradd", GOOS: "linux", Package: "passwd", Scope: ScopeNode, For: "kno bootstrap, kno doctor --fix",
		Why: "creates the updater account, and a service account a unit names",
	},
	{
		Name: "ssh-add", Package: "openssh-client", Scope: ScopePush,
		For: "kno release sign",
		Why: "lists what the ssh agent holds, so a signing key on a hardware token is found without its private half ever being on disk; only the machine that SIGNS needs it",
	},
	{
		Name: "ssh-keygen", Package: "openssh-client", Scope: ScopeNode,
		For: "kno activate, kno release",
		Why: "verifies the release signature against this host's allowed_signers before anything is installed; a host with an allowlist and no ssh-keygen refuses to install rather than installing unverified",
	},
	{
		Name: "podman", Scope: ScopeNode, GOOS: "linux", For: "kno activate (container backend)",
		Why:     "runs a node's containers; no daemon, and no group equivalent to root",
		Version: []string{"--version"},
	},
	{
		Name: "docker", Scope: ScopeNode, GOOS: "linux", AltFor: "podman",
		For:     "kno activate (container backend)",
		Why:     "runs a node's containers where podman is not installed; podman is preferred and needs no daemon",
		Version: []string{"--version"},
	},
	{
		Name: "usermod", GOOS: "linux", Package: "passwd", Scope: ScopeNode, For: "kno bootstrap",
		Why: "gives an updater account created before git-shell was used one that can serve a push",
	},
	{
		Name: "git-shell", GOOS: "linux", Package: "git", Scope: ScopeNode, For: "kno bootstrap (push-to-deploy)",
		Why: "the updater account's shell, so 'git push kno@host' can run git-receive-pack; without it that channel cannot work and is not built",
	},
	{
		Name: "dpkg-query", GOOS: "linux", Package: "dpkg", Scope: ScopeNode, For: "kno doctor",
		Why: "asks which packages own a service, so a node assembled by hand is named as that rather than as a dozen separate faults",
	},
	{
		Name: "runuser", GOOS: "linux", Package: "util-linux", Scope: ScopeNode, For: "kno doctor",
		Why: "re-runs a failing unit's config validation as the account systemd would, so a config the service user cannot read is caught",
	},
	{
		Name: "journalctl", GOOS: "linux", Package: "systemd", Scope: ScopeNode, For: "kno doctor",
		Why: "reads the last lines a failing unit printed, so doctor can say why it is down and not just that it is",
	},
	{
		Name: "groupadd", GOOS: "linux", Package: "passwd", Scope: ScopeNode, For: "kno doctor --fix",
		Why: "creates the group a service account belongs to; without it a unit fails 217/USER",
	},
	{
		Name: "chmod", Package: "coreutils", Scope: ScopeNode, For: "kno doctor --fix",
		Why: "narrows a service config to 0640 so only the service account can read it",
	},
	{
		Name: "xz", Package: "xz-utils", Scope: ScopeBuild, For: "kno freebsd vm tests",
		Why: "expands the 845MB VM image to 3.7GB across every core (-T0); the Go decoder kno reads packages with uses one core",
	},

	{
		Name: "mv", Package: "coreutils", Scope: ScopeNode, For: "kno doctor --fix",
		Why: "adds a suffix to the name of a unit file that has priority over the packaged unit, and puts it back if the unit does not start",
	},

	{
		Name: "ln", Scope: ScopeNode, GOOS: "freebsd",
		For: "kno activate (freebsd-ezjail backend)",
		Why: "points each service jail at the root that it runs from its next start, and records the root that it runs",
	},
	{
		Name: "mkdir", Scope: ScopeRemote, For: "kno push",
		Why: "creates the staging directory on the host being deployed",
	},
}

type Status struct {
	Tool    Tool   `json:"-"`
	Name    string `json:"name"`
	Program string `json:"program"`
	Env     string `json:"env,omitempty"`
	Scope   Scope  `json:"scope"`
	For     string `json:"for"`
	Why     string `json:"why"`
	Found   bool   `json:"found"`
	Path    string `json:"path,omitempty"`
	Version string `json:"version,omitempty"`
	Checked bool   `json:"checked"`
}

func (t Tool) Program() string {
	if t.Env != "" {
		if v := os.Getenv(t.Env); v != "" {
			return v
		}
	}
	return t.Name
}

func Check(t Tool) Status {
	s := Status{
		Tool: t, Name: t.Name, Program: t.Program(),
		Env: t.Env, Scope: t.Scope, For: t.For, Why: t.Why,
	}
	if t.Scope == ScopeRemote || t.Scope == ScopeVendored {
		return s
	}
	s.Checked = true
	prog := s.Program
	if t.Name == "kno" {
		prog = knopath.Running()
	}
	p, err := lookAdmin(prog)
	if err != nil {
		return s
	}
	s.Found, s.Path = true, p
	s.Version = version(t, p)
	return s
}

var sbinDirs = []string{"/usr/local/sbin", "/usr/sbin", "/sbin"}

func lookAdmin(program string) (string, error) {
	p, err := exec.LookPath(program)
	if err == nil {
		return p, nil
	}
	if strings.ContainsRune(program, os.PathSeparator) {
		return "", err
	}
	for _, d := range sbinDirs {
		c := filepath.Join(d, program)
		if fi, e := os.Stat(c); e == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return c, nil
		}
	}
	return "", err
}

func CheckAll() []Status {
	out := make([]Status, 0, len(Tools))
	for _, t := range Tools {
		if !t.wanted() {
			continue
		}
		out = append(out, Check(t))
	}
	return dropSatisfiedAlternatives(out)
}

func dropSatisfiedAlternatives(all []Status) []Status {
	found := map[string]bool{}
	for _, s := range all {
		if s.Found {
			found[s.Name] = true
		}
	}
	out := make([]Status, 0, len(all))
	for _, s := range all {
		if s.Tool.AltFor != "" && found[s.Tool.AltFor] {
			continue
		}
		out = append(out, s)
	}
	return out
}

func version(t Tool, path string) string {
	if len(t.Version) == 0 {
		return ""
	}
	cmd := exec.Command(path, t.Version...)
	var b []byte
	var err error
	if t.VersionOnStderr {
		b, err = cmd.CombinedOutput()
	} else {
		b, err = cmd.Output()
	}
	if err != nil && len(b) == 0 {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	return strings.TrimSpace(line)
}

func reportTools(r *Result, all []Status) {
	r.section("required commands")
	byScope := map[Scope][]Status{}
	for _, s := range all {
		byScope[s.Scope] = append(byScope[s.Scope], s)
	}
	for _, sc := range []Scope{ScopeCore, ScopeNode, ScopeBuild, ScopePush, ScopeVendored, ScopeRemote} {
		group := byScope[sc]
		if len(group) == 0 {
			continue
		}
		sort.SliceStable(group, func(i, j int) bool { return group[i].Name < group[j].Name })
		r.line("   %s: %s", sc, scopeMeaning(sc))
		for _, s := range group {
			r.line("     %s", toolLine(s))
		}
		if sc == ScopeCore {
			var missing []string
			for _, s := range group {
				if !s.Found {
					missing = append(missing, s.Program)
				}
			}
			r.check("core commands", len(missing) == 0, "%s", coreDetail(missing, len(group)))
		}
	}
}

func coreDetail(missing []string, total int) string {
	if len(missing) == 0 {
		return "all " + strconv.Itoa(total) + " present"
	}
	return "missing: " + strings.Join(missing, " ")
}

func scopeMeaning(s Scope) string {
	switch s {
	case ScopeCore:
		return "needed by every kno run"
	case ScopeNode:
		return "needed on a host that runs services"
	case ScopeBuild:
		return "needed to build and package"
	case ScopePush:
		return "needed to deploy from here to another host"
	case ScopeVendored:
		return "built by kno from the pinned source; not installed, not checked"
	case ScopeRemote:
		return "needed on the far side of an ssh; not checked here"
	}
	return ""
}

func toolLine(s Status) string {
	name := s.Program
	if s.Env != "" {
		name += " ($" + s.Env + ")"
	}
	switch {
	case !s.Checked:
		return pad(name, 34) + "not checked here"
	case !s.Found:
		miss := pad(name, 34) + "MISSING  -- " + s.For
		if p := s.Tool.Package; p != "" && p != s.Tool.Name {
			miss += " (install: " + p + ")"
		}
		return miss
	case s.Version != "":
		return pad(name, 34) + pad(s.Path, 26) + s.Version
	default:
		return pad(name, 34) + s.Path
	}
}

func pad(s string, n int) string {
	if len(s) >= n {
		return s + " "
	}
	return s + strings.Repeat(" ", n-len(s))
}
