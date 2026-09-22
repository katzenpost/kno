package sim

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/katzenpost/kno/internal/build"
	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/render"
	"github.com/katzenpost/kno/internal/service"
	"github.com/katzenpost/kno/internal/track"
)

const hostPackages = "systemd systemd-sysv openssh-server sudo rsync dbus git " + runnerPackages

func hostContainerfile(base string) string {
	return "FROM " + base + "\n" +
		"RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends " +
		hostPackages + " && rm -rf /var/lib/apt/lists/*\n" +
		"RUN mkdir -p /run/sshd && (systemctl enable ssh || systemctl enable sshd || true)\n" +
		"RUN mkdir -p /etc/systemd/system/systemd-journald.service.d && " +
		"printf '[Service]\\nImportCredential=\\nLoadCredential=\\n' > /etc/systemd/system/systemd-journald.service.d/sim.conf\n" +
		"STOPSIGNAL SIGRTMIN+3\n" +
		`CMD ["/sbin/init"]` + "\n"
}

func hostImage(p platform) string {
	return "localhost/kno-simhost-" + compile.OSToken(p.OS) + "-" + p.Goarch + ":2"
}

type rehearsal struct {
	Node string
	IP   string
	Old  string
	Ctr  string
	P    platform
	Svcs []daemon
	Pull bool
	Fail bool
}

type upgradeEnv struct {
	root      string
	prev      string
	next      string
	dir       string
	repo      string
	artifacts string
	conf      string
	kp        string
	sshDir    string
	bin       string
	nodeConf  string
	nodeArt   string
	home      string
	hosts     []rehearsal
}

func (r *run) upgradeDirs() (upgradeEnv, error) {
	d := filepath.Join(r.Work, "upgrade")
	debs, _ := filepath.Glob(filepath.Join(r.From, "katzenpost_*.deb"))
	var tags []string
	for _, d := range debs {
		if t := tagOfDeb(d); t != "" && !slices.Contains(tags, t) {
			tags = append(tags, t)
		}
	}
	if len(tags) == 0 {
		return upgradeEnv{}, fmt.Errorf("no katzenpost package in %s to take the previous release from", r.From)
	}
	if len(tags) > 1 {
		sort.Strings(tags)
		return upgradeEnv{}, fmt.Errorf("%s holds the katzenpost packages of %s; give --from the"+
			" packages of one release", r.From, strings.Join(tags, " "))
	}
	prev := tags[0]
	pins, err := r.pins()
	if err != nil {
		return upgradeEnv{}, err
	}
	next := pins["KATZENPOST_REF"]
	if prev == next {
		return upgradeEnv{}, fmt.Errorf("--from holds %s, the release this tree builds; nothing to upgrade", prev)
	}
	return upgradeEnv{
		prev:      prev,
		next:      next,
		dir:       d,
		root:      filepath.Join(d, "checkout"),
		repo:      filepath.Join(d, "checkout", filepath.Base(r.Repo)),
		artifacts: filepath.Join(d, "artifacts"),
		conf:      filepath.Join(d, "laptop.toml"),
		kp:        filepath.Join(d, "katzenpost"),
		sshDir:    filepath.Join(d, "ssh"),
		bin:       filepath.Join(d, "bin"),
		nodeConf:  filepath.Join(d, "node"),
		nodeArt:   filepath.Join(d, "node-artifacts"),
		home:      filepath.Join(d, "home"),
	}, nil
}

func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })
}

func (r *run) rehearsals(svcs []daemon) ([]rehearsal, error) {
	rehearsed := map[string]bool{}
	for _, n := range splitList(r.Rehearse) {
		rehearsed[n] = true
	}
	pull := map[string]bool{}
	for _, n := range splitList(r.Pull) {
		if !rehearsed[n] {
			return nil, fmt.Errorf("--pull names %s, which --rehearse (%s) does not upgrade", n, r.Rehearse)
		}
		pull[n] = true
	}
	if r.Fail != "" {
		switch {
		case !rehearsed[r.Fail]:
			return nil, fmt.Errorf("--fail names %s, which --rehearse (%s) does not upgrade;"+
				" give --fail a rehearsed node, or --fail '' for no rollback", r.Fail, r.Rehearse)
		case pull[r.Fail]:
			return nil, fmt.Errorf("--fail and --pull both name %s; a node that upgrades with"+
				" kno update does not rehearse the rollback", r.Fail)
		}
	}
	var out []rehearsal
	for _, n := range splitList(r.Rehearse) {
		h := rehearsal{Node: n, Pull: pull[n], Fail: n == r.Fail, Ctr: r.Net + "_h_" + n}
		for _, s := range svcs {
			if nodeOf(s.Base) != n {
				continue
			}
			if h.IP != "" && h.IP != s.IP {
				return nil, fmt.Errorf("node %s announces two addresses (%s and %s)", n, h.IP, s.IP)
			}
			h.IP, h.Old, h.P = s.IP, s.Ctr, r.platformOf(s)
			h.Svcs = append(h.Svcs, s)
		}
		if len(h.Svcs) == 0 {
			return nil, fmt.Errorf("node %s has no service in %s", n, r.Network)
		}
		for _, s := range svcs {
			if s.Ctr == h.Old && nodeOf(s.Base) != n {
				return nil, fmt.Errorf("node %s shares its address with %s; the rehearsal"+
					" replaces the whole container, so choose another node", n, s.Base)
			}
		}
		out = append(out, h)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no node to rehearse (--rehearse is empty)")
	}
	return out, nil
}

func (r *run) upgradeStage() error {
	if r.From == "" {
		return fmt.Errorf("the upgrade rehearsal needs --from, the directory of the previous release's packages")
	}
	if !r.containerExists(r.kpc) {
		return fmt.Errorf("the network %s is not up; run 'up' first", r.Net)
	}
	if err := r.installKno(); err != nil {
		return err
	}
	svcs, err := r.deriveSpec()
	if err != nil {
		return err
	}
	hs, err := r.rehearsals(svcs)
	if err != nil {
		return err
	}
	u, err := r.upgradeDirs()
	if err != nil {
		return err
	}
	u.hosts = hs
	r.log.Logf("rehearsing the upgrade from %s (%s) to %s (this tree)", u.prev, r.From, u.next)
	start := time.Now()
	if err := r.prepareUpgrade(u, hs); err != nil {
		return err
	}
	if err := r.laptopKno(u, nil, "build"); err != nil {
		return fmt.Errorf("kno build on the laptop side: %w", err)
	}
	for _, h := range hs {
		t := time.Now()
		if err := r.startOnPrevious(u, h, svcs); err != nil {
			return fmt.Errorf("%s: %w", h.Node, err)
		}
		r.log.Logf("%s: runs katzenpost %s on %s (%s)", h.Node, r.installedVersion(h), h.P, since(t))
	}
	if err := r.probeSettled("with the "+u.prev+" nodes", hs); err != nil {
		return err
	}
	for _, h := range hs {
		t := time.Now()
		var err error
		switch {
		case h.Pull:
			if err = r.pullUpgrade(u, h); err == nil {
				err = r.pullConfigChange(u, h)
			}
		case h.Fail:
			err = r.failThenUpgrade(u, h)
		default:
			err = r.pushUntilSettled(u, h, u.next)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", h.Node, err)
		}
		r.log.Logf("%s: runs katzenpost %s after the upgrade (%s)", h.Node, r.installedVersion(h), since(t))
	}
	if err := r.probeSettled("after the upgrades", hs); err != nil {
		return err
	}
	r.log.Logf("upgrade rehearsal passed in %s: %s", since(start), rehearsalSummary(hs))
	return nil
}

func rehearsalSummary(hs []rehearsal) string {
	var s []string
	for _, h := range hs {
		how := "push from the laptop side"
		if h.Pull {
			how = "kno update on the node, build made on " + hostGoarch
		}
		if h.Fail {
			how += ", forced verify failure rolled back first"
		}
		s = append(s, fmt.Sprintf("%s (%s, %s)", h.Node, h.P, how))
	}
	return strings.Join(s, "; ")
}

func removeAll(dir string) error {
	if err := os.RemoveAll(dir); err == nil {
		return nil
	}
	out, err := podmanRun("unshare", "rm", "-rf", dir)
	if err != nil {
		return fmt.Errorf("removing %s: %w\n%s", dir, err, out)
	}
	return nil
}

func (r *run) prepareUpgrade(u upgradeEnv, hs []rehearsal) error {
	if err := removeAll(u.dir); err != nil {
		return err
	}
	for _, d := range []string{u.dir, u.artifacts, u.sshDir, u.bin, u.nodeConf, u.nodeArt, u.home} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	if err := r.simRepo(u); err != nil {
		return err
	}
	if err := fakeKatzenpost(u.kp, u.prev, u.next); err != nil {
		return err
	}
	if err := r.sshSetup(u, hs); err != nil {
		return err
	}
	if err := r.laptopConfig(u, hs); err != nil {
		return err
	}
	if err := simOnly(u.conf, hs); err != nil {
		return err
	}
	if err := r.previousRelease(u, hs); err != nil {
		return err
	}
	var plats []platform
	for _, h := range hs {
		plats = append(plats, h.P)
	}
	if err := r.ensureEmulation(plats); err != nil {
		return err
	}
	seen := map[platform]bool{}
	for _, h := range hs {
		if seen[h.P] {
			continue
		}
		seen[h.P] = true
		if err := r.buildHostImage(h.P); err != nil {
			return err
		}
	}
	return nil
}

func (r *run) simRepo(u upgradeEnv) error {
	src := filepath.Dir(r.Repo)
	if _, err := os.Stat(filepath.Join(src, "go.mod")); err != nil {
		return fmt.Errorf("the rehearsal needs a kno checkout, and %s has no go.mod: kno update"+
			" builds the kno package from the checkout's source", src)
	}
	netRel, err := filepath.Rel(src, knopath.NetworkDir(r.Repo, r.Network))
	if err != nil {
		return err
	}
	if err := os.RemoveAll(u.root); err != nil {
		return err
	}
	if err := copyTree(src, u.root, ".git", "dist", ".ci-local", ".gocache", ".gomodcache", netRel); err != nil {
		return err
	}
	netDir := knopath.NetworkDir(u.repo, r.Network)
	if err := copyTree(r.net, netDir); err != nil {
		return err
	}
	if err := render.Generate(netDir, ""); err != nil {
		return fmt.Errorf("render the rehearsal's network: %w", err)
	}
	if err := os.WriteFile(filepath.Join(u.repo, track.VersionsFile),
		[]byte(u.prev+"\n"+u.next+"\n"), 0o644); err != nil {
		return err
	}
	return gitSnapshot(u.root)
}

func gitEnv() []string {
	return append(os.Environ(),
		"GIT_AUTHOR_NAME=kno sim", "GIT_AUTHOR_EMAIL=sim@kno.invalid",
		"GIT_COMMITTER_NAME=kno sim", "GIT_COMMITTER_EMAIL=sim@kno.invalid",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
}

func gitSnapshot(dir string) error {
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "-A"},
		{"commit", "-q", "-m", "simulated network"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = gitEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s in %s: %v\n%s", args[0], dir, err, out)
		}
	}
	return nil
}

func fakeKatzenpost(dir string, tags ...string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("tags for kno track\n"), 0o644); err != nil {
		return err
	}
	if err := gitSnapshot(dir); err != nil {
		return err
	}
	for _, t := range tags {
		cmd := exec.Command("git", "-C", dir, "tag", t)
		cmd.Env = gitEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git tag %s: %v\n%s", t, err, out)
		}
	}
	return nil
}

func (r *run) sshSetup(u upgradeEnv, hs []rehearsal) error {
	key := filepath.Join(u.sshDir, "id_ed25519")
	cmd := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "kno-sim", "-f", key)
	cmd.Env = scrubbed(os.Environ(), u.home)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ssh-keygen: %v\n%s", err, out)
	}
	return r.writeWrappers(u, hs)
}

func (r *run) writeWrappers(u upgradeEnv, hs []rehearsal) error {
	ssh, scp := "/usr/bin/ssh", "/usr/bin/scp"
	for _, p := range []string{ssh, scp} {
		if !executable(p) {
			return fmt.Errorf("the rehearsal needs %s", p)
		}
	}
	var cases strings.Builder
	for _, h := range hs {
		fmt.Fprintf(&cases, "\t%s) ctr=%s ;;\n", h.Node, h.Ctr)
	}
	wrapper := fmt.Sprintf(`#!/bin/sh
node=""
for a in "$@"; do
	case "$a" in
	sim-*|*@sim-*) n=${a#*@}; n=${n#sim-}; node=${n%%%%:*} ;;
	esac
done
ctr=""
case "$node" in
%s	*) echo "kno sim: refusing ssh outside the sim network: $*" >&2; exit 255 ;;
esac
exec %s -F /dev/null -o BatchMode=yes -o IdentityAgent=none -o IdentitiesOnly=yes \
	-i %s -o UserKnownHostsFile=%s -o StrictHostKeyChecking=no \
	-o ControlMaster=no -o ControlPath=none \
	-o "ProxyCommand=env HOME=%s %s exec -i $ctr nc 127.0.0.1 22" "$@"
`, cases.String(), ssh, filepath.Join(u.sshDir, "id_ed25519"),
		filepath.Join(u.sshDir, "known_hosts"), realHome(), compile.Podman())
	if err := os.WriteFile(filepath.Join(u.bin, "ssh"), []byte(wrapper), 0o755); err != nil {
		return err
	}
	scpw := "#!/bin/sh\nexec " + scp + " -S " + filepath.Join(u.bin, "ssh") + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(u.bin, "scp"), []byte(scpw), 0o755); err != nil {
		return err
	}
	fetchw := fmt.Sprintf("#!/bin/sh\nexec env HOME=%s %s exec -e %s %s %s \"$@\"\n",
		realHome(), compile.Podman(), r.epochEnv(), r.kpc, r.tool(roleFetch))
	return os.WriteFile(filepath.Join(u.bin, "kpfetch"), []byte(fetchw), 0o755)
}

const (
	sshdFault = "/etc/ssh/sshd_config.d/kno-sim-fault.conf"
	faultEnv  = "KNO_TEST=1 VERIFY_STUB=fail"
)

const emulatedDropIn = "/etc/systemd/system/service.d/kno-sim-emulated.conf"

const noSandbox = "[Service]\nDynamicUser=no\nPrivateTmp=no\nProtectSystem=no\nProtectHome=no\n" +
	"ProtectKernelTunables=no\nProtectKernelModules=no\nProtectControlGroups=no\nPrivateDevices=no\n" +
	"RestrictAddressFamilies=\nMemoryDenyWriteExecute=no\nLockPersonality=no\nRestrictRealtime=no\n" +
	"SystemCallFilter=\n"

func (r *run) hostFiles(pub string, h rehearsal) map[string]string {
	files := map[string]string{
		"/root/.ssh/authorized_keys":              pub + "\n",
		"/etc/environment":                        r.epochEnv() + "\n",
		"/etc/systemd/system.conf.d/kno-sim.conf": managerEnv(r.epochEnv()),
	}
	if h.Fail {
		files[sshdFault] = "SetEnv " + faultEnv + "\n"
	}
	if h.P.Goarch != hostGoarch {
		files[emulatedDropIn] = noSandbox
	}
	return files
}

var reConfigFlag = regexp.MustCompile(`\s-c\s+(\S+)`)

func configFlag(execStart string) string {
	if m := reConfigFlag.FindStringSubmatch(execStart); m != nil {
		return m[1]
	}
	return ""
}

func (r *run) hostClient(h rehearsal) error {
	out, err := podmanRun("exec", h.Ctr, "systemctl", "show", "-p", "ExecStart", service.ClientUnit)
	if err != nil {
		return fmt.Errorf("%s: %w\n%s", service.ClientUnit, err, out)
	}
	path := configFlag(out)
	if path == "" {
		return fmt.Errorf("%s names no -c config: %s", service.ClientUnit, strings.TrimSpace(out))
	}
	lines, err := readLines(filepath.Join(r.sim, "client.toml"))
	if err != nil {
		return err
	}
	if err := r.putFile(h.Ctr, path, strings.Join(lines, "\n")+"\n"); err != nil {
		return err
	}
	if out, err := r.hostExec(h.Ctr, "systemctl reset-failed "+service.ClientUnit+" 2>/dev/null; systemctl restart "+service.ClientUnit); err != nil {
		return fmt.Errorf("restart %s: %w\n%s", service.ClientUnit, err, out)
	}
	return nil
}

func (r *run) stageFile(body string) (string, error) {
	tmp, err := os.CreateTemp(r.Work, ".put-")
	if err != nil {
		return "", err
	}
	_, err = tmp.WriteString(body)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o644)
	}
	if err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

func (r *run) putFile(ctr, path, body string) error {
	name, err := r.stageFile(body)
	if err != nil {
		return err
	}
	defer os.Remove(name)
	stage := "/run/kno-sim-put"
	if err := podmanQuiet("cp", name, ctr+":"+stage); err != nil {
		return err
	}
	place := "mv " + stage + " " + path
	if path == "/etc/environment" {
		place = "cat " + stage + " >> " + path + " && rm " + stage
	}
	if out, err := r.hostExec(ctr, "mkdir -p "+filepath.Dir(path)+" && "+place); err != nil {
		return fmt.Errorf("writing %s on %s: %w\n%s", path, ctr, err, out)
	}
	return nil
}

func managerEnv(kv string) string {
	return "[Manager]\nDefaultEnvironment=" + kv + "\n"
}

func (r *run) rejoinTries(hs []rehearsal) int {
	n := max(r.probeTries(), 12)
	for _, h := range hs {
		if h.P.Goarch != hostGoarch {
			return max(n, int((10*r.Epoch)/(30*time.Second)), 40)
		}
	}
	return n
}

func realHome() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "/nonexistent"
	}
	return h
}

func scrubbed(env []string, home string) []string {
	var out []string
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "SSH_AUTH_SOCK", "SSH_AGENT_PID", "SSH_ASKPASS", "SSH_ASKPASS_REQUIRE", "DISPLAY",
			"WAYLAND_DISPLAY", "GIT_SSH", "GIT_SSH_COMMAND", "GIT_ASKPASS", "HOME", "PATH",
			"KNO_CONFIG", "KNO_CACHE", "SSH", "SCP", "RSYNC", "XDG_DATA_HOME", "XDG_CONFIG_HOME":
			continue
		}
		out = append(out, kv)
	}
	real := realHome()
	return append(out, "HOME="+home, "SSH_ASKPASS=/bin/false", "SSH_ASKPASS_REQUIRE=never",
		"XDG_DATA_HOME="+filepath.Join(real, ".local", "share"),
		"XDG_CONFIG_HOME="+filepath.Join(real, ".config"),
		"GIT_SSH_COMMAND=false", "GIT_TERMINAL_PROMPT=0", "KNO_CACHE="+knopath.Cache())
}

func simOnly(conf string, hs []rehearsal) error {
	c, err := config.Load(conf)
	if err != nil {
		return err
	}
	want := map[string]bool{}
	for _, h := range hs {
		want[h.Node] = true
	}
	for label, h := range c.Hosts {
		if !want[label] || h.SSH != "root@sim-"+label {
			return fmt.Errorf("refusing the rehearsal: %s names host %s (ssh %q), which is not a sim container",
				conf, label, h.SSH)
		}
	}
	if len(c.Hosts) == 0 {
		return fmt.Errorf("refusing the rehearsal: %s names no host", conf)
	}
	return nil
}

func (r *run) laptopConfig(u upgradeEnv, hs []rehearsal) error {
	var b strings.Builder
	var nodes []string
	for _, h := range hs {
		nodes = append(nodes, fmt.Sprintf("%q", h.Node))
	}
	fmt.Fprintf(&b, "network = %q\nnodes = [%s]\n\n[update]\npolicy = \"apply\"\n\n[push]\nmode = \"full\"\n\n",
		r.Network, strings.Join(nodes, ", "))
	fmt.Fprintf(&b, "[defaults]\nprivilege = \"none\"\nscheduler = \"none\"\n\n")
	for _, h := range hs {
		var svcs []string
		for _, s := range h.Svcs {
			svcs = append(svcs, fmt.Sprintf("%q", s.Base))
		}
		fmt.Fprintf(&b, "[hosts.%q]\nssh = \"root@sim-%s\"\nscheduler = \"none\"\nservices = [%s]\n\n",
			h.Node, h.Node, strings.Join(svcs, ", "))
	}
	if err := os.WriteFile(u.conf, []byte(b.String()), 0o644); err != nil {
		return err
	}
	for _, h := range hs {
		if h.Pull {
			continue
		}
		st := knopath.HostState(filepath.Join(u.artifacts, "state"), h.Node)
		if err := os.MkdirAll(st, 0o755); err != nil {
			return err
		}
		for _, f := range []string{"current", "last-known-good"} {
			if err := os.WriteFile(filepath.Join(st, f), []byte(u.prev+"\n"), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *run) laptopEnv(u upgradeEnv) []string {
	return append(scrubbed(os.Environ(), u.home),
		"PATH="+u.bin+":"+os.Getenv("PATH"),
		"KNO_CONFIG="+u.conf,
		"KNO_REPO="+u.repo,
		"KNO_ARTIFACTS="+u.artifacts,
		"KATZENPOST_REPO="+u.kp,
		"KNO_GIT_FETCH=0",
		"KNO_CLONE=0",
		"KNO_TRACK_FETCH=0",
		"KNO_SSH_MULTIPLEX=0",
		"SSH="+filepath.Join(u.bin, "ssh"),
		"SCP="+filepath.Join(u.bin, "scp"),
		"KNO_FETCH="+filepath.Join(u.bin, "kpfetch"),
		"VERIFY_THINCLIENT=/sim/thinclient.toml",
		"VERIFY_WINDOW_SECS="+fmt.Sprint(int((10*r.Epoch)/time.Second)),
		r.epochEnv(),
		"GIT_AUTHOR_NAME=kno sim", "GIT_AUTHOR_EMAIL=sim@kno.invalid",
		"GIT_COMMITTER_NAME=kno sim", "GIT_COMMITTER_EMAIL=sim@kno.invalid",
		"GIT_CONFIG_GLOBAL=/dev/null",
	)
}

func (r *run) laptopKno(u upgradeEnv, extra []string, args ...string) error {
	if err := simOnly(u.conf, u.hosts); err != nil {
		return err
	}
	cmd := exec.Command(filepath.Join(r.bin, "kno"), args...)
	cmd.Env = append(r.laptopEnv(u), extra...)
	cmd.Stdout, cmd.Stderr = r.Out, r.Out
	return cmd.Run()
}

func nodeOnly(conf, node string) error {
	c, err := config.Load(conf)
	if err != nil {
		return err
	}
	for label, h := range c.Hosts {
		if label != node || h.SSH != "" {
			return fmt.Errorf("refusing the rehearsal: the node config %s names host %s (ssh %q);"+
				" a node config may name only itself, with no ssh", conf, label, h.SSH)
		}
	}
	return nil
}

func findDeb(dir, pattern string) (string, error) {
	hits, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return "", fmt.Errorf("no %s in %s", pattern, dir)
	}
	sort.Strings(hits)
	return hits[len(hits)-1], nil
}

func (r *run) previousRelease(u upgradeEnv, hs []rehearsal) error {
	rel := filepath.Join(u.artifacts, "releases", u.prev)
	pkgs := filepath.Join(rel, "pkgs")
	for _, d := range []string{pkgs, filepath.Join(rel, "manifest")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	for _, h := range hs {
		arch := h.P.debArch()
		kp, err := findKatzenpost(r.From, h.P)
		if err != nil {
			return fmt.Errorf("the %s release: %w", u.prev, err)
		}
		if err := copyFile(kp, filepath.Join(pkgs, filepath.Base(kp)), 0o644); err != nil {
			return err
		}
		for _, pat := range []string{
			"kno_*_" + arch + ".deb",
			"kno-host-config-" + h.Node + "_*_" + arch + ".deb",
		} {
			deb, err := findDeb(r.From, pat)
			if err != nil {
				return fmt.Errorf("the %s release: %w", u.prev, err)
			}
			if err := copyFile(deb, filepath.Join(pkgs, filepath.Base(deb)), 0o644); err != nil {
				return err
			}
		}
		hc, err := findDeb(pkgs, "kno-host-config-"+h.Node+"_*_"+arch+".deb")
		if err != nil {
			return err
		}
		ex := filepath.Join(u.nodeConf, h.Node)
		if out, err := exec.Command("dpkg-deb", "-x", hc, ex).CombinedOutput(); err != nil {
			return fmt.Errorf("dpkg-deb -x %s: %v\n%s", filepath.Base(hc), err, out)
		}
		for _, s := range h.Svcs {
			deb, err := findDeb(r.From, configPkgPattern(s.Base, arch))
			if err != nil {
				return fmt.Errorf("the %s release: %w", u.prev, err)
			}
			if err := r.rekey(s.Base, deb, pkgs); err != nil {
				return fmt.Errorf("rekey %s: %w", filepath.Base(deb), err)
			}
		}
	}
	if err := writeSums(pkgs); err != nil {
		return err
	}
	return copyTree(rel, filepath.Join(u.nodeArt, "releases", u.prev))
}

func (r *run) rekey(svc, deb, outDir string) error {
	tmp, err := os.MkdirTemp(filepath.Dir(outDir), ".rekey-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if out, err := exec.Command("dpkg-deb", "-R", deb, tmp).CombinedOutput(); err != nil {
		return fmt.Errorf("dpkg-deb -R: %v\n%s", err, out)
	}
	swapped := 0
	ctl := filepath.Join(tmp, "DEBIAN")
	err = filepath.Walk(tmp, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || strings.HasPrefix(p, ctl) {
			return err
		}
		var src string
		switch filepath.Ext(p) {
		case ".pem":
			src = findByName(r.keys, filepath.Base(p))
		case ".toml":
			var legacy bool
			src, legacy = r.renderedFor(svc, p, tmp)
			if src != "" && legacy {
				if r.oldLayout == nil {
					r.oldLayout = map[string]bool{}
				}
				r.oldLayout[oldLayoutKey(svc, p)] = true
				swapped++
				return copyKeepingPaths(src, p, fi.Mode().Perm())
			}
		}
		if src == "" {
			return nil
		}
		swapped++
		return copyFile(src, p, fi.Mode().Perm())
	})
	if err != nil {
		return err
	}
	if swapped == 0 {
		return fmt.Errorf("it carries no key and no config to swap")
	}
	_ = os.Remove(filepath.Join(ctl, "md5sums"))
	out, err := exec.Command("dpkg-deb", "--root-owner-group", "-b", tmp,
		filepath.Join(outDir, filepath.Base(deb))).CombinedOutput()
	if err != nil {
		return fmt.Errorf("dpkg-deb -b: %v\n%s", err, out)
	}
	return nil
}

func findByName(root, name string) string {
	found := ""
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err == nil && found == "" && !fi.IsDir() && filepath.Base(p) == name {
			found = p
		}
		return nil
	})
	return found
}

func (r *run) renderedFor(svc, path, root string) (src string, legacy bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", false
	}
	p := "/" + filepath.ToSlash(rel)
	if filepath.Base(path) == "courier.toml" {
		return filepath.Join(r.cfg, "courier.toml"), !strings.HasPrefix(p, filepath.Dir(service.CourierConfig)+"/")
	}
	s, err := service.ResolveService(r.Repo, r.Network, svc)
	switch {
	case err != nil:
		return "", false
	case s.ConfigDst == p:
		return filepath.Join(r.cfg, svc+".toml"), false
	case s.LegacyConfigDst == p:
		return filepath.Join(r.cfg, svc+".toml"), true
	}
	return "", false
}

var layoutKey = regexp.MustCompile(`(?m)^(\s*(DataDir|Command|c|File)\s*=\s*)"([^"]*)"`)

func oldLayoutKey(svc, path string) string {
	if filepath.Base(path) == "courier.toml" {
		return svc + " courier"
	}
	return svc
}

func copyKeepingPaths(src, dst string, mode os.FileMode) error {
	old, err := os.ReadFile(dst)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	kept := map[string][]string{}
	for _, m := range layoutKey.FindAllSubmatch(old, -1) {
		kept[string(m[2])] = append(kept[string(m[2])], string(m[3]))
	}
	used := map[string]int{}
	out := layoutKey.ReplaceAllFunc(b, func(line []byte) []byte {
		m := layoutKey.FindSubmatch(line)
		k := string(m[2])
		if used[k] >= len(kept[k]) {
			return line
		}
		v := kept[k][used[k]]
		used[k]++
		return []byte(string(m[1]) + `"` + v + `"`)
	})
	return os.WriteFile(dst, out, mode)
}

func writeSums(dir string) error {
	debs, err := filepath.Glob(filepath.Join(dir, "*.deb"))
	if err != nil {
		return err
	}
	sort.Strings(debs)
	var b strings.Builder
	for _, d := range debs {
		sum, _, err := fsutil.SHA256File(d)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s  %s\n", sum, filepath.Base(d))
	}
	return os.WriteFile(filepath.Join(dir, build.SumsName), []byte(b.String()), 0o644)
}

func (r *run) buildHostImage(p platform) error {
	return r.buildFrom("host", hostImage(p), p, hostContainerfile)
}

func (r *run) hostExec(ctr, script string) (string, error) {
	return podmanRun("exec", ctr, "sh", "-c", script)
}

func (r *run) startOnPrevious(u upgradeEnv, h rehearsal, all []daemon) error {
	_ = podmanQuiet("rm", "-f", "-t", "1", h.Old)
	_ = podmanQuiet("rm", "-f", "-t", "1", h.Ctr)
	if err := podmanQuiet("run", "-d", "--replace", "--name", h.Ctr, "--hostname", "sim-"+h.Node,
		"--platform", "linux/"+h.P.Goarch, "--systemd=always", "--cap-add", "SYS_ADMIN",
		"--network", r.Net, "--cap-add", "NET_ADMIN", "--label", r.Net+"=1",
		"-v", compile.MustMount(u.nodeArt, u.nodeArt+":rw"),
		"-v", compile.MustMount(u.root, u.root+":rw"),
		"-v", compile.MustMount(u.kp, u.kp+":ro"),
		hostImage(h.P)); err != nil {
		return err
	}
	if err := r.waitSystemd(h.Ctr); err != nil {
		return err
	}
	r.addRoutes(h.Ctr, h.IP, serviceIPs(all))
	if err := r.hostNAT(h); err != nil {
		return err
	}
	pub, err := os.ReadFile(filepath.Join(u.sshDir, "id_ed25519.pub"))
	if err != nil {
		return err
	}
	if h.P.Goarch != hostGoarch {
		r.log.Warnf("%s: systemd under qemu-user cannot sandbox a unit (226/NAMESPACE), so this"+
			" emulated host runs its units without DynamicUser and the Protect* settings;"+
			" their sandboxing is tested only on the native hosts", h.Node)
	}
	for path, body := range r.hostFiles(strings.TrimSpace(string(pub)), h) {
		if err := r.putFile(h.Ctr, path, body); err != nil {
			return err
		}
	}
	if out, err := r.hostExec(h.Ctr, `set -e
chmod 700 /root/.ssh && chmod 600 /root/.ssh/authorized_keys
systemctl daemon-reexec
systemctl restart ssh 2>/dev/null || systemctl restart sshd
systemctl show-environment | grep -qx '`+r.epochEnv()+`'`); err != nil {
		return fmt.Errorf("preparing the host: systemd does not carry %s: %w\n%s", r.epochEnv(), err, out)
	}
	if err := r.seedPrivateKeys(h); err != nil {
		return err
	}
	return r.installPrevious(u, h)
}

func (r *run) waitSystemd(ctr string) error {
	for i := 0; i < 150; i++ {
		out, _ := podmanRun("exec", ctr, "systemctl", "is-system-running")
		if s := strings.TrimSpace(out); s == "running" || s == "degraded" {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("systemd in %s did not finish starting", ctr)
}

var reV6 = regexp.MustCompile(`tcp://\[([0-9A-Fa-f:]+)\]:`)

func ownV6(lines []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range ownBlock(lines) {
		if !reAddrLine.MatchString(l) && !reBindLine.MatchString(l) {
			continue
		}
		for _, m := range reV6.FindAllStringSubmatch(l, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				out = append(out, m[1])
			}
		}
	}
	return out
}

func (r *run) hostNAT(h rehearsal) error {
	for _, s := range h.Svcs {
		lines, err := readLines(filepath.Join(r.cfg, s.Base+".toml"))
		if err != nil {
			return err
		}
		for _, a := range ownV6(lines) {
			if out, err := r.hostExec(h.Ctr, "ip -6 addr add "+a+"/128 dev eth0 nodad 2>&1 | grep -v 'File exists' || true"); err != nil {
				return fmt.Errorf("adding %s to %s: %w\n%s", a, h.Ctr, err, out)
			}
		}
		own := ownBlock(lines)
		_, nat := bindFor(addrsOn(own, reAddrLine), addrsOn(own, reBindLine), false)
		var nl []string
		for _, n := range nat {
			nl = append(nl, strings.Join([]string{n.AnnIP, n.AnnPort, n.BindIP, n.BindPort}, " "))
		}
		if err := writeLines(filepath.Join(r.sim, s.Base+".nat"), nl, 0o644); err != nil {
			return err
		}
		if err := r.wireNAT(h.Ctr, s.Base); err != nil {
			return err
		}
	}
	return nil
}

func (r *run) seedPrivateKeys(h rehearsal) error {
	for _, s := range h.Svcs {
		dd, err := r.dataDirOf(s.Base)
		if err != nil {
			return err
		}
		if r.oldLayout[oldLayoutKey(s.Base, s.Base+".toml")] {
			sv, err := service.ResolveService(r.Repo, r.Network, s.Base)
			if err != nil {
				return err
			}
			dd = sv.LegacyStateDir
		}
		if err := r.copyKeysInto(h.Ctr, filepath.Join(r.dd, s.DataSrc), dd); err != nil {
			return err
		}
	}
	for _, s := range h.Svcs {
		if !strings.HasSuffix(s.Base, "-pq-servicenode") {
			continue
		}
		src := filepath.Join(r.dd, "courier-"+strings.TrimSuffix(s.Base, "-pq-servicenode"))
		if !fsutil.IsDir(src) {
			continue
		}
		lines, err := readLines(filepath.Join(r.cfg, "courier.toml"))
		if err != nil {
			return err
		}
		dd := tomlValue(lines, "DataDir")
		if dd == "" {
			return fmt.Errorf("courier.toml names no DataDir")
		}
		if r.oldLayout[oldLayoutKey(s.Base, "courier.toml")] {
			dd = service.LegacyCourierState
		}
		if err := r.copyKeysInto(h.Ctr, src, dd); err != nil {
			return err
		}
	}
	return nil
}

func tomlValue(lines []string, key string) string {
	for _, l := range lines {
		if keyLine(l, key) {
			if m := reIdent.FindStringSubmatch(l); m != nil {
				return m[1]
			}
		}
	}
	return ""
}

func (r *run) dataDirOf(base string) (string, error) {
	lines, err := readLines(filepath.Join(r.cfg, base+".toml"))
	if err != nil {
		return "", err
	}
	if dd := tomlValue(ownBlock(lines), "DataDir"); dd != "" {
		return dd, nil
	}
	return "", fmt.Errorf("%s names no DataDir", base)
}

func (r *run) copyKeysInto(ctr, src, dst string) error {
	if out, err := r.hostExec(ctr, "mkdir -p "+dst+" && chmod 700 "+dst); err != nil {
		return fmt.Errorf("%w\n%s", err, out)
	}
	pems, err := filepath.Glob(filepath.Join(src, "*.pem"))
	if err != nil {
		return err
	}
	for _, p := range pems {
		if err := podmanQuiet("cp", p, ctr+":"+filepath.Join(dst, filepath.Base(p))); err != nil {
			return err
		}
	}
	return nil
}

func (r *run) installPrevious(u upgradeEnv, h rehearsal) error {
	pkgs := filepath.Join(u.nodeArt, "releases", u.prev, "pkgs")
	arch := h.P.debArch()
	kp, err := findKatzenpost(pkgs, h.P)
	if err != nil {
		return err
	}
	names := []string{kp}
	for _, pat := range []string{
		"kno_*_" + arch + ".deb",
		"kno-host-config-" + h.Node + "_*_" + arch + ".deb",
	} {
		d, err := findDeb(pkgs, pat)
		if err != nil {
			return err
		}
		names = append(names, d)
	}
	for _, s := range h.Svcs {
		d, err := findDeb(pkgs, configPkgPattern(s.Base, arch))
		if err != nil {
			return err
		}
		names = append(names, d)
	}
	out, err := podmanRun(append([]string{"exec", h.Ctr, "dpkg", "-i"}, names...)...)
	if err != nil {
		return fmt.Errorf("installing %s: %w\n%s", u.prev, err, out)
	}
	if err := r.hostLogDirs(h); err != nil {
		return err
	}
	if err := r.hostClient(h); err != nil {
		return err
	}
	if h.Pull {
		if err := r.seedNodeState(u, h); err != nil {
			return err
		}
	}
	return r.unitsActive(h)
}

func logDirs(lines []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range lines {
		if !keyLine(l, "File") {
			continue
		}
		m := reIdent.FindStringSubmatch(l)
		if m == nil || !strings.HasPrefix(m[1], "/") {
			continue
		}
		if d := filepath.Dir(m[1]); !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

func (r *run) hostLogDirs(h rehearsal) error {
	for _, s := range h.Svcs {
		sv, err := service.ResolveService(r.Repo, r.Network, s.Base)
		if err != nil {
			return err
		}
		files := []string{filepath.Join(r.cfg, s.Base+".toml")}
		if strings.HasSuffix(s.Base, "-pq-servicenode") {
			files = append(files, filepath.Join(r.cfg, "courier.toml"))
		}
		for _, f := range files {
			lines, err := readLines(f)
			if err != nil {
				continue
			}
			for _, d := range logDirs(lines) {
				if _, err := r.hostExec(h.Ctr, "test -d "+d); err == nil {
					continue
				}
				unit := loadedUnit(h, sv)
				script := fmt.Sprintf(`u=$(systemctl show -p User --value %s); mkdir -p %s && chown "${u:-root}" %s && chmod 0750 %s`,
					unit, d, d, d)
				if out, err := r.hostExec(h.Ctr, script); err != nil {
					return fmt.Errorf("log directory %s: %w\n%s", d, err, out)
				}
				r.log.Warnf("%s: no package creates %s, which %s logs to; made it as the host would have it",
					h.Node, d, filepath.Base(f))
				if out, err := r.hostExec(h.Ctr, "systemctl reset-failed "+unit+"; systemctl restart "+unit); err != nil {
					return fmt.Errorf("restart %s: %w\n%s", unit, err, out)
				}
			}
		}
	}
	return nil
}

func (r *run) seedNodeState(u upgradeEnv, h rehearsal) error {
	st := filepath.Join(u.nodeArt, "state")
	script := fmt.Sprintf("mkdir -p %s && printf '%%s\\n' %s > %s/current && printf '%%s\\n' %s > %s/last-known-good",
		st, u.prev, st, u.prev, st)
	if out, err := r.hostExec(h.Ctr, script); err != nil {
		return fmt.Errorf("%w\n%s", err, out)
	}
	return nil
}

func loadedUnit(h rehearsal, sv service.Service) string {
	if sv.LegacyUnit == "" {
		return sv.Unit
	}
	if out, _ := podmanRun("exec", h.Ctr, "systemctl", "show", "-p", "LoadState", sv.Unit); strings.Contains(out, "LoadState=loaded") {
		return sv.Unit
	}
	return sv.LegacyUnit
}

func steady(a, b string) bool {
	return strings.Contains(a, "ActiveState=active\n") && a == b
}

func (r *run) unitsActive(h rehearsal) error {
	var units []string
	for _, s := range h.Svcs {
		sv, err := service.ResolveService(r.Repo, r.Network, s.Base)
		if err != nil {
			return err
		}
		units = append(units, loadedUnit(h, sv))
	}
	show := func(u string) string {
		out, _ := podmanRun("exec", h.Ctr, "systemctl", "show", "-p", "ActiveState,NRestarts", u)
		return out
	}
	var bad []string
	for i := 0; i < 20; i++ {
		bad = nil
		first := map[string]string{}
		for _, u := range units {
			first[u] = show(u)
		}
		time.Sleep(10 * time.Second)
		for _, u := range units {
			if !steady(first[u], show(u)) {
				bad = append(bad, u)
			}
		}
		if len(bad) == 0 {
			return nil
		}
	}
	out, _ := podmanRun("exec", h.Ctr, "sh", "-c",
		"systemctl --no-pager --failed; journalctl --no-pager -n 30 -u '"+bad[0]+"'")
	return fmt.Errorf("not running steadily on %s: %s\n%s", h.Ctr, strings.Join(bad, " "), out)
}

func (r *run) probeSettled(why string, hs []rehearsal) error {
	tries := r.rejoinTries(hs)
	var err error
	for i := 1; i <= tries; i++ {
		if err = r.probeOnce(); err == nil {
			return nil
		}
		r.log.Warnf("probe %s: attempt %d of %d found a discrepancy; the node may still be rejoining", why, i, tries)
		time.Sleep(30 * time.Second)
	}
	return fmt.Errorf("probe %s: %w", why, err)
}

func (r *run) installedVersion(h rehearsal) string {
	out, _ := podmanRun("exec", h.Ctr, "dpkg-query", "-W", "-f", "${Version}", "katzenpost")
	return strings.TrimSpace(out)
}

func (r *run) laptopUpdate(u upgradeEnv, label string) error {
	return r.laptopKno(u, nil, "update", "--host", label)
}

func (r *run) laptopState(u upgradeEnv, label string) (state, current string) {
	st := knopath.HostState(filepath.Join(u.artifacts, "state"), label)
	b, _ := os.ReadFile(filepath.Join(st, "state"))
	c, _ := os.ReadFile(filepath.Join(st, "current"))
	return strings.TrimSpace(string(b)), strings.TrimSpace(string(c))
}

func (r *run) laptopQuarantined(u upgradeEnv, label, tag string) bool {
	b, _ := os.ReadFile(filepath.Join(knopath.HostState(filepath.Join(u.artifacts, "state"), label), "quarantine"))
	for _, ln := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(ln) == tag {
			return true
		}
	}
	return false
}

const settleTicks = 12

func versionIs(v, tag string) bool {
	t := strings.TrimPrefix(tag, "v")
	return v == t || strings.HasPrefix(v, t+"+") || strings.HasPrefix(v, t+"-")
}

func configPkgPattern(svc, arch string) string {
	return "*-config-" + svc + "_*_" + arch + ".deb"
}

func findKatzenpost(dir string, p platform) (string, error) {
	debs, err := filepath.Glob(filepath.Join(dir, "katzenpost_*.deb"))
	if err != nil {
		return "", err
	}
	if d := pickDeb(debs, p); d != "" {
		return d, nil
	}
	return "", fmt.Errorf("no katzenpost package for %s in %s", p, dir)
}

func (r *run) pushUntilSettled(u upgradeEnv, h rehearsal, want string) error {
	for i := 1; i <= settleTicks; i++ {
		r.log.Logf("%s: kno update --host %s from the laptop side (run %d)", h.Node, h.Node, i)
		if err := r.laptopUpdate(u, h.Node); err != nil {
			r.log.Warnf("%s: run %d exited with %v", h.Node, i, err)
		}
		state, cur := r.laptopState(u, h.Node)
		if state != "pending" && cur == want {
			if v := r.installedVersion(h); !versionIs(v, want) {
				return fmt.Errorf("kno says %s, and the host has katzenpost %s", want, v)
			}
			return r.unitsActive(h)
		}
		time.Sleep(r.Epoch / 2)
	}
	return fmt.Errorf("not settled on %s after %d ticks", want, settleTicks)
}

func (r *run) failThenUpgrade(u upgradeEnv, h rehearsal) error {
	r.log.Logf("%s: the host's kno verify fails on purpose (%s)", h.Node, faultEnv)
	rolled := false
	for i := 1; i <= settleTicks; i++ {
		r.log.Logf("%s: kno update --host %s from the laptop side (run %d)", h.Node, h.Node, i)
		if err := r.laptopUpdate(u, h.Node); err != nil {
			r.log.Warnf("%s: run %d exited with %v", h.Node, i, err)
		}
		state, cur := r.laptopState(u, h.Node)
		if state != "pending" && cur == u.prev && r.laptopQuarantined(u, h.Node, u.next) {
			rolled = true
			break
		}
		if state != "pending" && cur == u.next {
			return fmt.Errorf("kno promoted %s although the host's kno verify was forced to fail", u.next)
		}
		time.Sleep(r.Epoch / 2)
	}
	if !rolled {
		return fmt.Errorf("the forced failure did not roll back to %s", u.prev)
	}
	if v := r.installedVersion(h); !versionIs(v, u.prev) {
		return fmt.Errorf("kno rolled back, and the host has katzenpost %s", v)
	}
	if err := r.unitsActive(h); err != nil {
		return err
	}
	if err := r.probeSettled("after the rollback", []rehearsal{h}); err != nil {
		return err
	}
	r.log.Logf("%s: rolled back to %s and in consensus; now the upgrade without the fault", h.Node, u.prev)
	if out, err := r.hostExec(h.Ctr, "rm -f "+sshdFault+" && (systemctl restart ssh 2>/dev/null || systemctl restart sshd)"); err != nil {
		return fmt.Errorf("%w\n%s", err, out)
	}
	if err := r.laptopKno(u, nil, "unquarantine", "--host", h.Node, u.next); err != nil {
		return fmt.Errorf("kno unquarantine %s: %w", u.next, err)
	}
	return r.pushUntilSettled(u, h, u.next)
}

func (r *run) nodeEnv(u upgradeEnv, h rehearsal) []string {
	env := []string{
		"KNO_HOST=" + h.Node,
		"KNO_CONFIG=" + filepath.Join(u.nodeConf, h.Node, "etc", "katzenpost", "kno.toml"),
		"KNO_REPO=" + u.repo,
		"KNO_ARTIFACTS=" + u.nodeArt,
		"RELEASES_DIR=" + filepath.Join(u.nodeArt, "releases"),
		"UPDATER_STATE_DIR=" + filepath.Join(u.nodeArt, "state"),
		"KATZENPOST_REPO=" + u.kp,
		"KNO_TRACK_FETCH=0", "KNO_CLONE=0", "SUDO=",
		"GIT_AUTHOR_NAME=kno sim", "GIT_AUTHOR_EMAIL=sim@kno.invalid",
		"GIT_COMMITTER_NAME=kno sim", "GIT_COMMITTER_EMAIL=sim@kno.invalid",
		"VERIFY_WINDOW_SECS=" + fmt.Sprint(int((10*r.Epoch)/time.Second)),
		r.epochEnv(),
	}
	if h.P.Goarch != hostGoarch {
		env = append(env, "VERIFY_FETCH_TIMEOUT=600")
	}
	return env
}

func (r *run) nodeUpdateArgs(u upgradeEnv, h rehearsal) []string {
	args := []string{"exec"}
	for _, kv := range r.nodeEnv(u, h) {
		args = append(args, "-e", kv)
	}
	return append(args, h.Ctr, "kno", "update")
}

func (r *run) pullUpgrade(u upgradeEnv, h rehearsal) error {
	if err := r.prebuildForNode(u, h, ""); err != nil {
		return err
	}
	if out, err := r.hostExec(h.Ctr, "mkdir -p "+u.nodeConf); err != nil {
		return fmt.Errorf("%w\n%s", err, out)
	}
	if err := podmanQuiet("cp", filepath.Join(u.nodeConf, h.Node), h.Ctr+":"+u.nodeConf+"/"); err != nil {
		return err
	}
	kno, err := findDeb(filepath.Join(u.nodeArt, "releases", u.next, "pkgs"), "kno_*_"+h.P.debArch()+".deb")
	if err != nil {
		return err
	}
	r.log.Logf("%s: the operator installs the new kno on the node: %s", h.Node, filepath.Base(kno))
	if out, err := podmanRun("exec", h.Ctr, "dpkg", "-i", kno); err != nil {
		return fmt.Errorf("installing the new kno: %w\n%s", err, out)
	}
	st := filepath.Join(u.nodeArt, "state")
	for i := 1; i <= settleTicks; i++ {
		r.log.Logf("%s: kno update on the node (tick %d)", h.Node, i)
		_ = r.podmanStream(r.nodeUpdateArgs(u, h)...)
		b, _ := os.ReadFile(filepath.Join(st, "state"))
		c, _ := os.ReadFile(filepath.Join(st, "current"))
		if i > 1 && strings.TrimSpace(string(b)) != "pending" && strings.TrimSpace(string(c)) == u.next {
			if v := r.installedVersion(h); !versionIs(v, u.next) {
				return fmt.Errorf("kno says %s, and the node has katzenpost %s", u.next, v)
			}
			return r.unitsActive(h)
		}
		time.Sleep(r.Epoch / 2)
	}
	return fmt.Errorf("kno update on the node did not settle on %s", u.next)
}

var logLevel = regexp.MustCompile(`(?m)^(\s*Level\s*=\s*)"[A-Z]+"`)

func (r *run) configVersion(h rehearsal, svc string) string {
	out, _ := podmanRun("exec", h.Ctr, "dpkg-query", "-W", "-f", "${Version}", knopath.ConfigPkgPrefix+svc)
	return strings.TrimSpace(out)
}

func (r *run) pullConfigChange(u upgradeEnv, h rehearsal) error {
	if len(h.Svcs) == 0 {
		return fmt.Errorf("no service to change the config of")
	}
	svc := h.Svcs[0].Base
	cfg := filepath.Join(knopath.ConfigsDir(u.repo, r.Network), svc+".toml")
	b, err := os.ReadFile(cfg)
	if err != nil {
		return err
	}
	level := `"INFO"`
	if strings.Contains(string(b), `Level = "INFO"`) {
		level = `"NOTICE"`
	}
	changed := logLevel.ReplaceAll(b, []byte("${1}"+level))
	if string(changed) == string(b) {
		return fmt.Errorf("%s has no log level line to change", cfg)
	}
	if err := os.WriteFile(cfg, changed, 0o644); err != nil {
		return err
	}
	for _, args := range [][]string{{"add", cfg}, {"commit", "-q", "-m", "sim: a config change for " + svc}} {
		cmd := exec.Command("git", append([]string{"-C", u.root}, args...)...)
		cmd.Env = gitEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %v\n%s", args[0], err, out)
		}
	}
	before := r.configVersion(h, svc)
	r.log.Logf("%s: a config change for %s at %s; the node's kno update must install it", h.Node, svc, u.next)
	running := filepath.Join(u.dir, "node-kno-running-"+h.Node)
	if err := os.MkdirAll(running, 0o755); err != nil {
		return err
	}
	if err := podmanQuiet("cp", h.Ctr+":/usr/bin/kno", filepath.Join(running, "kno")); err != nil {
		return err
	}
	if err := r.prebuildForNode(u, h, filepath.Join(running, "kno")); err != nil {
		return err
	}
	st := filepath.Join(u.nodeArt, "state")
	saw := false
	for i := 1; i <= settleTicks; i++ {
		out, _ := podmanRun(r.nodeUpdateArgs(u, h)...)
		r.log.Logf("%s: kno update on the node (config tick %d)\n%s", h.Node, i, strings.TrimSpace(out))
		saw = saw || strings.Contains(out, "new config packages for "+svc)
		b, _ := os.ReadFile(filepath.Join(st, "state"))
		if i > 1 && strings.TrimSpace(string(b)) != "pending" {
			after := r.configVersion(h, svc)
			switch {
			case !saw:
				return fmt.Errorf("kno update never said that %s has new configs", svc)
			case after == before:
				return fmt.Errorf("%s is still at config version %s", svc, before)
			case !versionIs(r.installedVersion(h), u.next):
				return fmt.Errorf("the config change moved katzenpost off %s", u.next)
			}
			r.log.Logf("%s: %s went from config version %s to %s at katzenpost %s", h.Node, svc, before, after, u.next)
			return r.unitsActive(h)
		}
		time.Sleep(r.Epoch / 2)
	}
	return fmt.Errorf("kno update on the node did not settle after the config change for %s", svc)
}

func (r *run) prebuildForNode(u upgradeEnv, h rehearsal, knoOnNode string) error {
	conf := filepath.Join(u.nodeConf, h.Node, "etc", "katzenpost", "kno.toml")
	if err := nodeOnly(conf, h.Node); err != nil {
		return err
	}
	saved := map[string]*string{}
	for _, kv := range append(r.nodeEnv(u, h), "HOME="+u.home, "KNO_CACHE="+knopath.Cache(),
		"SSH=/bin/false", "GIT_SSH_COMMAND=false", "SSH_ASKPASS_REQUIRE=never") {
		k, v, _ := strings.Cut(kv, "=")
		if old, ok := os.LookupEnv(k); ok {
			saved[k] = &old
		} else {
			saved[k] = nil
		}
		_ = os.Setenv(k, v)
	}
	defer func() {
		for k, v := range saved {
			if v == nil {
				_ = os.Unsetenv(k)
			} else {
				_ = os.Setenv(k, *v)
			}
		}
	}()
	e, err := track.LoadEnv()
	if err != nil {
		return err
	}
	if len(e.Services) == 0 {
		return fmt.Errorf("the node's kno.toml places no service on %s", h.Node)
	}
	r.log.Logf("%s: building %s for %s on %s, as the node's own kno update would (%s)",
		h.Node, u.next, h.P, hostGoarch, strings.Join(e.Services, " "))
	o := build.Options{
		Repo:        e.Repo,
		Network:     e.Network,
		Tag:         u.next,
		Dist:        filepath.Dir(e.ReleasesDir),
		Services:    e.Services,
		Arches:      []string{h.P.debArch()},
		SkipFreeBSD: true,
		SkipDocker:  true,
		Verbose:     r.Verbose,
	}
	if _, err := build.Build(o); err != nil {
		return err
	}
	deb, err := findDeb(filepath.Join(o.Dist, "releases", u.next, "pkgs"), "kno_*_"+h.P.debArch()+".deb")
	if err != nil {
		return err
	}
	ex := filepath.Join(u.dir, "node-kno-"+h.Node)
	if err := os.RemoveAll(ex); err != nil {
		return err
	}
	if out, err := exec.Command("dpkg-deb", "-x", deb, ex).CombinedOutput(); err != nil {
		return fmt.Errorf("dpkg-deb -x %s: %v\n%s", filepath.Base(deb), err, out)
	}
	if knoOnNode == "" {
		knoOnNode = filepath.Join(ex, "usr", "bin", "kno")
	}
	if err := build.MarkBuiltFor(o, knoOnNode); err != nil {
		return err
	}
	r.log.Warnf("%s: the node's kno update finds this build as its own and does not compile:"+
		" the sim network has no internet and a %s Go build under qemu is not practical."+
		" The real node compiles natively", h.Node, h.P)
	return nil
}
