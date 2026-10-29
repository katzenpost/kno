package hostconfig

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/nodefile"
	"github.com/katzenpost/kno/internal/service"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/sign"
)

const perHostControl = `Package: kno-host-config-%s
Version: %s
Architecture: %s
Maintainer: %s
Provides: kno-host-config
Conflicts: kno-host-config
Replaces: kno-host-config
Section: admin
Priority: optional
Description: kno.toml for the %s host of the %s network
 Operator identity and update policy for this host, decided when the release
 was built: which %s services run here, how updates are applied and whether a
 signed tag is required. Installs /etc/katzenpost/kno.toml; your edits to it
 are kept.
`

const perHostPostinst = `#!/bin/sh
set -e
if [ "$1" = configure ]; then
	# Said once. $2 is the version being upgraded FROM, so it is empty exactly
	# on this host's first configure -- the one time an operator needs telling
	# what the machine has been set up to run and what to do next. Every
	# upgrade after that repeats it into a deploy log, and 'kno status' says
	# the same thing on demand.
	if [ -z "$2" ]; then
		echo "kno-host-config-%s: this host runs: %s" >&2
		echo "Next: 'kno status' to check for an update, then 'kno update' to apply it." >&2
	fi
	sched=%s
%s
	# SUDO= drops the sudo prefix: a postinst is already root, and a minimal
	# Debian host has no sudo installed at all.
	if [ "$sched" != none ] && command -v kno >/dev/null 2>&1; then
		SUDO= kno schedule "$sched" >&2 ||
			echo "kno-host-config-%s: could not install the $sched scheduler; run: sudo kno schedule $sched" >&2
	fi
fi
`

const accountCheck = `	if ! getent passwd %s >/dev/null 2>&1; then
		echo "kno-host-config-%s: kpclientd is configured to run as '%s', which does not" >&2
		echo "  exist on this host. systemd fails that with a bare status=217/USER and" >&2
		echo "  names nothing. Create the account, or clear kpclientd_user to use the" >&2
		echo "  packaged unit's dynamic user, then: sudo systemctl restart kpclientd" >&2
	fi
`

const clientRestart = `	if command -v systemctl >/dev/null 2>&1; then
		systemctl daemon-reload || true
		systemctl try-restart kpclientd.service || true
	fi
`

func debNameSafe(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '+', r == '.':
		default:
			return false
		}
	}
	return true
}

func sshHostname(ssh string) string {
	h := ssh
	if i := strings.LastIndexByte(h, '@'); i >= 0 {
		h = h[i+1:]
	}
	if i := strings.IndexByte(h, ':'); i >= 0 {
		h = h[:i]
	}
	return h
}

func ForHost(c config.Config, label string, repos ...string) config.Config {
	h := c.Hosts[label]
	out := config.Config{Network: c.Network, Peers: c.Peers}
	out.Update = c.Update
	out.Push = c.Push
	hostname := h.Hostname
	if hostname == "" {
		hostname = sshHostname(h.SSH)
	}
	if hostname == "" {
		hostname = label
	}
	kp := h.Kpclientd
	if kp == nil && !c.RunsKpclientd(label) {
		kp = config.Bool(false)
	}
	out.Hosts = map[string]config.Host{
		label: {Hostname: hostname, Scheduler: h.Scheduler, Services: h.Services, Kpclientd: kp, KpclientdUser: c.KpclientdUser(label), ClientJail: h.ClientJail},
	}
	seen := map[string]bool{}
	for _, s := range h.Services {
		n, _ := nodefile.NodeOf(s)
		if len(repos) > 0 {
			n, _ = config.ServiceNode(repos[0], config.NetworkOf(c), s)
		}
		if !seen[n] {
			seen[n] = true
			out.Nodes = append(out.Nodes, n)
		}
	}
	if len(out.Nodes) == 0 {
		out.Nodes = []string{label}
	}
	sort.Strings(out.Nodes)
	return out
}

func StageHost(c config.Config, label, root, repo, network, goos string) (config.Config, string, bool, error) {
	if _, ok := c.Hosts[label]; !ok {
		return config.Config{}, "", false, fmt.Errorf("no host %q in the operator config", label)
	}
	hc := ForHost(c, label, repo)
	if err := os.RemoveAll(root); err != nil {
		return config.Config{}, "", false, err
	}
	if err := config.Write(hc, filepath.Join(root, config.SystemConfigPathsFor(goos)[0])); err != nil {
		return config.Config{}, "", false, err
	}
	signed := false
	if signers, err := os.ReadFile(sign.RepoPath(repo, network)); err == nil {
		dst := filepath.Join(root, sign.NodePathFor(goos))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return config.Config{}, "", false, err
		}
		if err := os.WriteFile(dst, signers, 0o644); err != nil {
			return config.Config{}, "", false, err
		}
		signed = true
	}
	svcs := strings.Join(hc.Hosts[label].Services, " ")
	if svcs == "" {
		svcs = "(no services placed here)"
	}
	return hc, svcs, signed, nil
}

func AssembleHostRoot(c config.Config, label, version, rootParent, repo, network, arch string) (string, error) {
	if arch == "" {
		arch = deb.ArchAll
	}
	if !debNameSafe(label) {
		return "", fmt.Errorf("host label %q cannot be a Debian package name. Use lowercase letters, digits, - + .", label)
	}
	if version == "" {
		version = "0.0.0"
	}
	if network == "" {
		network = c.Network
	}
	if network == "" {
		network = "namenlos"
	}

	root := filepath.Join(rootParent, "kno-host-config-"+label)
	hc, svcs, signed, err := StageHost(c, label, root, repo, network, "linux")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(root, "DEBIAN"), 0o755); err != nil {
		return "", err
	}
	sched := hc.Hosts[label].Scheduler
	if sched == "" {
		sched = "none"
	}
	conffiles := []string{config.SystemConfigPathsFor("linux")[0]}
	if signed {
		conffiles = append(conffiles, sign.NodePathFor("linux"))
	}

	if c.RunsKpclientd(label) {
		if err := clientFiles(root, repo, network, c.KpclientdUser(label)); err != nil {
			return "", err
		}
	}

	client := ""
	if c.RunsKpclientd(label) {
		if u := c.KpclientdUser(label); u != "" {
			client = fmt.Sprintf(accountCheck, u, label, u)
		}
		client += clientRestart
	}
	files := []struct {
		name string
		body string
		mode os.FileMode
	}{
		{"control", fmt.Sprintf(perHostControl, label, version, arch, deb.Maintainer(), label, network, network), 0o644},

		{"conffiles", strings.Join(conffiles, "\n") + "\n", 0o644},
		{"postinst", fmt.Sprintf(perHostPostinst, label, svcs, sched, client, label), 0o755},
	}
	for _, f := range files {
		p := filepath.Join(root, "DEBIAN", f.name)
		if err := os.WriteFile(p, []byte(f.body), f.mode); err != nil {
			return "", err
		}
		if err := os.Chmod(p, f.mode); err != nil {
			return "", err
		}
	}
	return root, nil
}

func PackageForHost(ctx context.Context, c config.Config, label, version, outdir, repo, network, arch string) (string, error) {
	if arch == "" {
		arch = deb.ArchAll
	}
	if version == "" {
		version = "0.0.0"
	}
	parent := filepath.Join(outdir, ".pkgbuild")
	root, err := AssembleHostRoot(c, label, version, parent, repo, network, arch)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(outdir, 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(outdir, "kno-host-config-"+label+"_"+version+"_"+arch+".deb")
	if err := deb.Build(ctx, root, out); err != nil {
		return "", err
	}
	os.RemoveAll(parent)
	return out, nil
}

const ClientDropIn = "/usr/lib/systemd/system/kpclientd.service.d/kno.conf"

func clientFiles(root, repo, network, user string) error {
	if repo == "" {
		return fmt.Errorf("this host runs kpclientd, but no repo was given to read the client config from")
	}
	cfg, err := os.ReadFile(filepath.Join(knopath.ConfigsDir(repo, network), "client.toml"))
	if err != nil {
		return fmt.Errorf("kpclientd client config: %w", err)
	}
	dst := filepath.Join(root, service.KnoClientConfig)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dst, cfg, 0o644); err != nil {
		return err
	}
	drop := "[Service]\nExecStart=\nExecStart=" + service.ClientExec + " -c " + service.KnoClientConfig + "\n"
	if user != "" {
		drop += "DynamicUser=no\nUser=" + user + "\nGroup=" + user + "\n"
	}
	p := filepath.Join(root, ClientDropIn)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(drop), 0o644)
}
