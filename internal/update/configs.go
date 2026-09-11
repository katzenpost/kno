package update

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/pkg/deb"
)

func (r ToolRunner) Configs() string { return configsOf(r.Env.Repo, r.Env.Network, r.Env.Services) }

func configsOf(repo, network string, svcs []string) string {
	var out []string
	for _, svc := range svcs {
		out = append(out, svc+"="+deb.ConfigStamp(repo, network, svc))
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

func configsFile(stateDir string) string { return filepath.Join(stateDir, "configs") }

func failedConfigsFile(stateDir string) string { return filepath.Join(stateDir, "configs-failed") }

func configsFailed(stateDir, want string) bool {
	failed := readLine(failedConfigsFile(stateDir))
	return failed != "" && failed == want
}

func changedServices(installed, want string) []string {
	have := map[string]bool{}
	for _, kv := range strings.Fields(installed) {
		have[kv] = true
	}
	var out []string
	for _, kv := range strings.Fields(want) {
		if !have[kv] {
			svc, _, _ := strings.Cut(kv, "=")
			out = append(out, svc)
		}
	}
	return out
}

func snapshotName(tag string) string { return tag + ".last" }

func (s *session) snapshot(tag string) error {
	if s.dry {
		return nil
	}
	src := filepath.Join(s.o.Env.ReleasesDir, tag)
	dst := filepath.Join(s.o.Env.ReleasesDir, snapshotName(tag))
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if !fsutil.IsDir(src) {
		return nil
	}
	return fsutil.CopyTreeGuarded(src, dst)
}

func (s *session) dropSnapshot(tag string) error {
	if s.dry {
		return nil
	}
	return os.RemoveAll(filepath.Join(s.o.Env.ReleasesDir, snapshotName(tag)))
}

func (r *RemoteRunner) Configs() string { return configsOf(r.Env.Repo, r.Env.Network, r.Host.Services) }
