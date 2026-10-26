package hostsetup

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/service"

	repodata "github.com/katzenpost/kno/data"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/schedule"
	"github.com/katzenpost/kno/internal/track"
)

type BootstrapOptions struct {
	Repo string

	User string

	Home string

	Bare string

	KatzenpostURL string

	OperatorPubkey string

	BinDir string

	Deps *DepsOptions

	PackageInstall bool

	Privilege string
}

type Facts struct {
	UserExists       bool
	BareRepoExists   bool
	KatzenpostCloned bool
	HasSystemd       bool

	Kpclientd bool

	GitShell string

	UserShell string
}

func DefaultBootstrapOptions(repo string) BootstrapOptions {
	return BootstrapOptions{
		Repo:          repo,
		User:          schedule.DefaultUser,
		Home:          "/var/lib/kno-updater",
		Bare:          "/srv/kno.git",
		KatzenpostURL: "https://github.com/katzenpost/katzenpost",
		BinDir:        schedule.DefaultBinDir,
	}
}

const SystemConfPath = "/etc/katzenpost/kno.toml"

const (
	ServicePath = "/etc/systemd/system/kno-update.service"
	TimerPath   = "/etc/systemd/system/kno-update.timer"
)

func PostReceiveHook(home, bare string) string {
	return fmt.Sprintf(`#!/bin/sh
set -eu
work="%s/network-organizer"
if [ -d "$work/.git" ]; then
	git -C "$work" fetch --tags --prune --quiet origin
else
	git clone --quiet "%s" "$work"
fi
`, home, bare)
}

func embeddedUnit(name, user, bindir string) string {
	b, err := fs.ReadFile(repodata.FS(), "systemd/"+name)
	if err != nil {

		panic("kno: embedded " + name + " is missing: " + err.Error())
	}
	return schedule.ExpandUnit(string(b), user, bindir)
}

func updateService(user, bindir string) string {
	return embeddedUnit("kno-update.service", user, bindir)
}

func updateTimer(user, bindir string) string {
	return embeddedUnit("kno-update.timer", user, bindir)
}

func Probe(o BootstrapOptions) Facts {
	u, userErr := user.Lookup(o.User)
	_, systemctlErr := exec.LookPath("systemctl")
	shell := ""
	if userErr == nil && u != nil {
		shell = loginShell(u.Username)
	}
	return Facts{
		GitShell:         findGitShell(),
		UserShell:        shell,
		UserExists:       userErr == nil,
		BareRepoExists:   fsutil.IsDir(o.Bare),
		KatzenpostCloned: fsutil.IsDir(filepath.Join(o.Home, "katzenpost", ".git")),
		HasSystemd:       systemctlErr == nil,
		Kpclientd:        wantsKpclientd(),
	}
}

var gitShellPaths = []string{"/usr/bin/git-shell", "/usr/local/bin/git-shell", "/bin/git-shell"}

func findGitShell() string {
	if p, err := exec.LookPath("git-shell"); err == nil {
		return p
	}
	for _, p := range gitShellPaths {
		if exists(p) {
			return p
		}
	}
	return ""
}

func loginShell(name string) string {
	b, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(line, ":")
		if len(f) >= 7 && f[0] == name {
			return f[6]
		}
	}
	return ""
}

func updaterShell(o BootstrapOptions, f Facts) string {
	if o.PackageInstall || f.GitShell == "" {
		return "/usr/sbin/nologin"
	}
	return f.GitShell
}

func wantsKpclientd() bool {
	if !exists(service.KnoClientConfig) {
		return false
	}
	e, err := track.LoadEnv()
	return err == nil && clientWanted(e)
}

func clientWanted(e track.Env) bool {
	if len(e.Services) == 0 {
		return false
	}
	if label, _, ok := config.LocalHost(e.Config); ok {
		return e.Config.RunsKpclientd(label)
	}

	return true
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func (o BootstrapOptions) check() error {
	required := [][2]string{
		{"User", o.User}, {"Home", o.Home},
		{"BinDir", o.BinDir},
	}
	if !o.PackageInstall {

		required = append(required,
			[2]string{"Repo", o.Repo}, [2]string{"Bare", o.Bare},
			[2]string{"KatzenpostURL", o.KatzenpostURL})
	}
	var missing []string
	for _, f := range required {
		if f[1] == "" {
			missing = append(missing, f[0])
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("hostsetup: unset option(s): %s", strings.Join(missing, ", "))
	}
	return nil
}

func withoutWriteTo(p Plan, path string) Plan {
	out := make(Plan, 0, len(p))
	for _, s := range p {
		if s.File != nil && s.File.Path == path {
			continue
		}
		out = append(out, s)
	}
	return out
}

func BootstrapPlan(o BootstrapOptions, f Facts) (Plan, error) {
	if err := o.check(); err != nil {
		return nil, err
	}

	p := RepairSteps(o)

	if f.HasSystemd {
		p = withoutWriteTo(p, under(ServicePath))
	}

	owner := o.User + ":" + o.User
	shell := updaterShell(o, f)
	if !f.UserExists {
		p = append(p, Step{Desc: "create the updater user", Argv: []string{
			"useradd", "--system", "--home-dir", o.Home, "--create-home",
			"--shell", shell, o.User}})
	} else if f.UserShell != "" && f.UserShell != shell && shell != "/usr/sbin/nologin" {

		p = append(p, Step{
			Desc: "give the updater account a shell that can serve git (was " + f.UserShell + ")",
			Argv: []string{"usermod", "--shell", shell, o.User}})
	}
	p = append(p, Step{Desc: "create the updater state dirs", Argv: []string{
		"install", "-d", "-o", o.User, "-g", o.User,
		o.Home, filepath.Join(o.Home, "releases"), filepath.Join(o.Home, "state")}})

	if !o.PackageInstall && f.GitShell != "" {
		if !f.BareRepoExists {
			p = append(p, Step{Desc: "create the bare repo", Argv: []string{
				"git", "init", "--quiet", "--bare", o.Bare}})
		}
		p = append(p, Step{Desc: "own the bare repo", Argv: []string{
			"chown", "-R", owner, o.Bare}})
		p = append(p, Step{Desc: "install the post-receive hook", File: &File{
			Path:    filepath.Join(o.Bare, "hooks", "post-receive"),
			Content: PostReceiveHook(o.Home, o.Bare),
			Mode:    0o755,
			Owner:   owner,
		}})
	}

	if o.OperatorPubkey != "" {
		ssh := filepath.Join(o.Home, ".ssh")
		keys := filepath.Join(ssh, "authorized_keys")
		p = append(p,
			Step{Desc: "create the updater .ssh dir", Argv: []string{
				"install", "-d", "-m", "0700", "-o", o.User, "-g", o.User, ssh}},

			Step{Desc: "authorize the operator key", File: &File{
				Path:   keys,
				Source: o.OperatorPubkey,
				Mode:   0o600,
				Owner:  owner,
				Append: true,
			}},
		)
	}

	if !f.KatzenpostCloned && !o.PackageInstall {
		p = append(p, Step{Desc: "clone katzenpost", Argv: []string{
			"runuser", "-u", o.User, "--", "git", "clone", "--quiet",
			o.KatzenpostURL, filepath.Join(o.Home, "katzenpost")}})
	}

	p = append(p, Step{Desc: "create the system config dir", Argv: []string{
		"install", "-d", "-m", "0755", filepath.Dir(SystemConfPath)}})

	if f.HasSystemd {
		p = append(p,
			Step{Desc: "install the updater unit", File: &File{
				Path: ServicePath, Content: updateService(o.User, o.BinDir), Mode: 0o644}},
			Step{Desc: "install the updater timer", File: &File{
				Path: TimerPath, Content: updateTimer(o.User, o.BinDir), Mode: 0o644}},
			Step{Desc: "reload systemd", Argv: []string{"systemctl", "daemon-reload"}},
		)

		if !o.PackageInstall {
			p = append(p, Step{Desc: "enable the updater timer", Argv: []string{
				"systemctl", "enable", "--now", "kno-update.timer"}})
		}

		if f.Kpclientd {
			p = append(p,
				Step{Desc: "clear any earlier kpclientd failure", BestEffort: true,
					Argv: []string{"systemctl", "reset-failed", "kpclientd.service"}},
				Step{Desc: "enable the client daemon", BestEffort: true,
					Argv: []string{"systemctl", "enable", "--now", "kpclientd.service"}},
			)
		}
	}
	return p, nil
}

func EnvBootstrapOptions(repo string) BootstrapOptions {
	o := DefaultBootstrapOptions(repo)
	for _, e := range []struct {
		name string
		dst  *string
	}{
		{"KNO_USER", &o.User}, {"KNO_HOME", &o.Home}, {"KNO_BARE", &o.Bare},
		{"KATZENPOST_URL", &o.KatzenpostURL}, {"OPERATOR_PUBKEY", &o.OperatorPubkey},
		{"KNO_PRIVILEGE", &o.Privilege},
	} {
		if v := os.Getenv(e.name); v != "" {
			*e.dst = v
		}
	}

	privSource := "KNO_PRIVILEGE"
	if o.Privilege == "" {
		if p := config.ResolvePath(""); p != "" {
			if c, err := config.Load(p); err == nil {
				if label, _, ok := config.LocalHost(c); ok {
					o.Privilege, privSource = c.Privilege(label), p
				}
			}
		}
	}

	if o.Privilege != "" && !config.ValidPrivilege(o.Privilege) {
		fmt.Fprintf(os.Stderr, "kno: privilege %q (from %s) is not one of %s. kno uses %s\n",
			o.Privilege, privSource, strings.Join(config.Privileges, ", "), config.PrivNone)
		o.Privilege = config.PrivNone
	}
	if os.Getenv("BOOTSTRAP_SKIP_DEPS") != "1" {
		profile := os.Getenv("DEPS_PROFILE")
		if profile == "" {
			profile = ProfileFull
		}
		if m, err := DetectManager(); err == nil {
			o.Deps = &DepsOptions{Profile: profile, Manager: m, Packages: EnvPackages(m)}
		}
	}
	return o
}

func checkRevision(repo, rev string) error {
	if rev == "" {
		return nil
	}
	if _, ok := config.GitRoot(repo); !ok {
		return fmt.Errorf("--rev %s: %s is not in a git checkout, so kno cannot check its revision", rev, repo)
	}
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		return fmt.Errorf("rev-parse: %w", err)
	}
	if have := strings.TrimSpace(string(out)); have != rev {
		return fmt.Errorf("revision mismatch: have %s, want %s", have, rev)
	}
	out, err = exec.Command("git", "-C", repo, "status", "--porcelain", "--", ".").Output()
	if err != nil {
		return fmt.Errorf("git status: %w", err)
	}
	if changed := strings.TrimSpace(string(out)); changed != "" {
		return fmt.Errorf("%s has changes that are not in %s, so --rev does not describe it:\n%s", repo, rev, changed)
	}
	return nil
}
