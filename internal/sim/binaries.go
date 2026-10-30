package sim

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/katzenpost/kno/internal/activate"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/service"
)

func FindBinaries(repo, network, tag string) (dir, how string, cleanup func(), err error) {
	noop := func() {}
	if d := installedDir(repo, network); d != "" {
		return d, "installed on this host", noop, nil
	}
	deb, err := releaseDeb(repo, network, tag)
	if err != nil {
		return "", "", noop, err
	}
	tmp, err := os.MkdirTemp("", "kno-sim-bin-")
	if err != nil {
		return "", "", noop, err
	}
	if out, err := exec.Command("dpkg-deb", "-x", deb, tmp).CombinedOutput(); err != nil {
		os.RemoveAll(tmp)
		return "", "", noop, fmt.Errorf("extracting %s: %v: %s",
			filepath.Base(deb), err, strings.TrimSpace(string(out)))
	}

	return daemonDir(tmp),
		"extracted from " + filepath.Base(deb),
		func() { os.RemoveAll(tmp) }, nil
}

func installedDir(repo, network string) string {
	svcs, err := ConfiguredServices(repo, network)
	if err != nil {
		return ""
	}
	for _, svc := range svcs {
		s, err := service.ResolveService(repo, network, svc)
		if err != nil || s.BinaryDst == "" {
			continue
		}
		if _, err := os.Stat(s.BinaryDst); err == nil {
			return filepath.Dir(s.BinaryDst)
		}
	}
	return ""
}

func releaseDeb(repo, network, tag string) (string, error) {
	rel := filepath.Join(knopath.Artifacts(), "releases", tag, "pkgs")
	arch := activate.HostArch()
	hits, err := filepath.Glob(filepath.Join(rel, "katzenpost_*_"+arch+".deb"))
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return "", fmt.Errorf("no katzenpost package for %s in %s\n"+
			"  'kno build' makes one, or point --bin at a directory of daemons",
			arch, rel)
	}
	p := newRun(NetOptions{Repo: repo, Network: network}).clientPlatform()
	if deb := pickDeb(hits, p); deb != "" {
		return deb, nil
	}
	for _, hit := range hits {
		if strings.Contains(filepath.Base(hit), "+sim") {
			return "", fmt.Errorf("no native validation package for %s in %s", p, rel)
		}
	}
	return newestDeb(hits), nil
}

func daemonDir(root string) string {
	for _, d := range []string{filepath.Join(root, "usr", "bin"), filepath.Join(root, "usr", "local", "bin")} {
		if ents, err := os.ReadDir(d); err == nil && len(ents) > 0 {
			return d
		}
	}
	return filepath.Join(root, "usr", "bin")
}

func daemonFor(dir, build, dst string) string {
	aliases := map[string][]string{
		"pq-katzenpost-authority":       {"kpdirauth", "dirauth"},
		"pq-katzenpost-mixserver":       {"kpserver", "server"},
		"pq-katzenpost-storage-replica": {"kpreplica", "replica"},
		"pq-katzenpost-courier":         {"kpcourier", "courier"},
	}
	names := []string{filepath.Base(dst), "kp" + strings.TrimPrefix(build, "kp")}
	names = append(names, aliases[filepath.Base(dst)]...)
	for _, name := range names {
		if p := filepath.Join(dir, name); name != "kp" {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}
