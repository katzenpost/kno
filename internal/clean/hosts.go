package clean

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/sshx"
	"github.com/katzenpost/kno/internal/track"
)

const askTimeout = 30 * time.Second

type HostPlan struct {
	Label  string
	Node   string
	Keep   []string
	Remove []string

	Stale []string
	Err   error
}

func remoteRoot() string { return ".local/share/katzenpost/kno/pkgs" }

func PlanHosts(e track.Env, labels []string, ssh string) []HostPlan {
	var out []HostPlan
	for _, label := range labels {
		p := HostPlan{Label: label, Node: config.SSHTarget(e.Config, label)}
		if p.Node == "" {
			continue
		}

		keep := hostProtected(e, label)
		if len(keep) == 0 {

			p.Err = fmt.Errorf("no recorded current or last-known-good for %s;"+
				" refusing to guess what it may lose", label)
			out = append(out, p)
			continue
		}
		p.Keep = keep

		held, err := listRemote(ssh, p.Node)
		if err != nil {
			p.Err = err
			out = append(out, p)
			continue
		}
		kept := map[string]bool{}
		for _, t := range keep {
			kept[t] = true
		}
		for _, t := range held {
			if !kept[t] {
				p.Remove = append(p.Remove, t)
			}
		}
		sort.Strings(p.Remove)

		p.Stale = stale(ssh, p.Node, keep)
		out = append(out, p)
	}
	return out
}

func stale(ssh, node string, tags []string) []string {
	var out []string
	for _, tag := range tags {
		if !safeTag(tag) {
			continue
		}
		files, installed, err := listPackages(ssh, node, tag)
		if err != nil {
			return nil
		}
		for _, f := range superseded(files, installed) {
			out = append(out, tag+"/pkgs/"+f)
		}
	}
	sort.Strings(out)
	return out
}

func superseded(files []string, installed map[string]string) []string {
	byID := map[deb.PkgID][]string{}
	for _, f := range files {
		if !safeDeb(f) {
			continue
		}
		id := deb.IDOf(f)
		byID[id] = append(byID[id], f)
	}
	var out []string
	for _, group := range byID {
		if len(group) < 2 {
			continue
		}
		newest := deb.Newest(group)
		for _, f := range group {
			if f == newest {
				continue
			}
			name, _, ok := deb.SplitName(f)
			if ok && installed[name] == debVersion(f) {
				continue
			}
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

func debVersion(base string) string {
	trimmed, ok := strings.CutSuffix(base, ".deb")
	if !ok {
		return ""
	}
	fields := strings.Split(trimmed, "_")
	if len(fields) != 3 {
		return ""
	}
	return fields[1]
}

func safeDeb(s string) bool {
	return strings.HasSuffix(s, ".deb") && safeTag(s)
}

func listPackages(ssh, node, tag string) ([]string, map[string]string, error) {
	const sep = "=== installed"
	cmd := "ls -1 " + remoteRoot() + "/" + tag + "/pkgs 2>/dev/null || true; " +
		"echo '" + sep + "'; " +
		"dpkg-query -W -f='${Package} ${Version}\\n' 2>/dev/null || true"
	out, err := runSSH(ssh, node, cmd)
	if err != nil {
		return nil, nil, fmt.Errorf("%s did not answer: %w", node, err)
	}
	var files []string
	installed := map[string]string{}
	past := false
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == sep {
			past = true
			continue
		}
		if ln == "" {
			continue
		}
		if !past {
			if safeDeb(ln) {
				files = append(files, ln)
			}
			continue
		}
		if name, ver, ok := strings.Cut(ln, " "); ok {
			installed[name] = ver
		}
	}
	return files, installed, nil
}

func hostProtected(e track.Env, label string) []string {
	dir := knopath.HostState(e.StateDir, label)
	var out []string
	seen := map[string]bool{}
	for _, f := range []string{"current", "last-known-good"} {
		v := strings.TrimSpace(firstLine(filepath.Join(dir, f)))
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

func listRemote(ssh, node string) ([]string, error) {
	out, err := runSSH(ssh, node, "ls -1 "+remoteRoot()+" 2>/dev/null || true")
	if err != nil {
		return nil, fmt.Errorf("%s did not answer: %w", node, err)
	}
	var tags []string
	for _, ln := range strings.Split(out, "\n") {
		if ln = strings.TrimSpace(ln); ln != "" && safeTag(ln) {
			tags = append(tags, ln)
		}
	}
	sort.Strings(tags)
	return tags, nil
}

func safeTag(s string) bool {
	if s == "" || s == "." || s == ".." || len(s) > 128 {
		return false
	}

	if s[0] == '-' {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '-' || r == '_' || r == '+':
		default:
			return false
		}
	}
	return !strings.Contains(s, "..")
}

func ReportHosts(w io.Writer, plans []HostPlan) {
	for _, p := range plans {
		fmt.Fprintf(w, "  %-10s %s\n", p.Label, p.Node)
		if p.Err != nil {
			fmt.Fprintf(w, "      skipped: %v\n", p.Err)
			continue
		}
		fmt.Fprintf(w, "      keeping  %s\n", strings.Join(p.Keep, " "))
		if len(p.Remove) > 0 {
			fmt.Fprintf(w, "      would remove %d: %s\n", len(p.Remove), strings.Join(p.Remove, " "))
		}
		if len(p.Stale) > 0 {
			fmt.Fprintf(w, "      superseded packages in a kept release: %d\n", len(p.Stale))
			for _, s := range p.Stale {
				fmt.Fprintf(w, "        %s\n", s)
			}
		}
		if len(p.Remove) == 0 && len(p.Stale) == 0 {
			fmt.Fprintln(w, "      nothing else held")
		}
	}
}

func RemoveHosts(w io.Writer, plans []HostPlan, ssh string, dry bool) error {
	var failed int
	for _, p := range plans {
		if p.Err != nil || (len(p.Remove) == 0 && len(p.Stale) == 0) {
			continue
		}
		var paths []string
		for _, t := range p.Remove {
			if !safeTag(t) {
				continue
			}
			paths = append(paths, remoteRoot()+"/"+t)
		}

		for _, s := range p.Stale {
			if !safeStale(s) {
				continue
			}
			paths = append(paths, remoteRoot()+"/"+s)
		}
		if len(paths) == 0 {
			continue
		}
		cmd := "rm -rf -- " + strings.Join(paths, " ")
		if dry {
			fmt.Fprintf(w, "  DRYRUN %s: %s\n", p.Label, cmd)
			continue
		}
		if _, err := runSSH(ssh, p.Node, cmd); err != nil {
			fmt.Fprintf(w, "  %s: %v\n", p.Label, err)
			failed++
			continue
		}
		fmt.Fprintf(w, "  %s: removed %d release(s) and %d superseded package(s)\n",
			p.Label, len(p.Remove), len(p.Stale))
	}
	if failed > 0 {
		return fmt.Errorf("%d host(s) failed", failed)
	}
	return nil
}

func runSSH(ssh, node, remote string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), askTimeout)
	defer cancel()
	argv := sshx.SSH(ssh, node, remote)
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
	return string(out), err
}

func safeStale(s string) bool {
	parts := strings.Split(s, "/")
	if len(parts) != 3 || parts[1] != "pkgs" {
		return false
	}
	return safeTag(parts[0]) && safeDeb(parts[2])
}
