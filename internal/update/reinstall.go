package update

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/katzenpost/kno/internal/track"
)

type reinstallCheck interface{ CanReinstall(tag string) error }

func (s *session) canReinstall(tag string) bool {
	c, ok := s.o.Runner.(reinstallCheck)
	if !ok {
		return true
	}
	if err := c.CanReinstall(tag); err != nil {
		s.l.Warnf("kno does not install %s again: %v", tag, err)
		return false
	}
	return true
}

func (r *RemoteRunner) CanReinstall(tag string) error {
	if r.Before == nil || len(r.Before.Packages) == 0 {
		return fmt.Errorf("kno has no list of the packages that %s ran", r.Host.Label)
	}
	dir := filepath.Join(r.Env.ReleasesDir, tag, "pkgs")
	names := make([]string, 0, len(r.Before.Packages))
	for name := range r.Before.Packages {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if v := r.Before.Packages[name]; !staged(dir, name, v) {
			return fmt.Errorf("%s has no %s %s, which %s ran", dir, name, v, r.Host.Label)
		}
	}
	return nil
}

func staged(dir, name, version string) bool {
	for _, pat := range []string{name + "_" + version + "_*.deb", name + "_" + version + "+*_*.deb"} {
		if hits, _ := filepath.Glob(filepath.Join(dir, pat)); len(hits) > 0 {
			return true
		}
	}
	return false
}

func sameRelease(e track.Env, tag string) bool {
	return tag != "" && readLine(filepath.Join(e.StateDir, "last-known-good")) == tag
}
