package build

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/release"
)

func recordedClientBundle(o tarballOpts, bindir, token string, epoch int64, configs []release.TarballFile) (string, error) {
	kind := o.Kind
	if kind == "" {
		kind = NativeBuild
	}
	entries, err := committedEntries(o.Repo, o.Network, o.Tag, kind)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name > entries[j].Name })
	dir := filepath.Join(o.Rel, "pkgs")
	for _, p := range entries {
		if filepath.Base(p.Name) != p.Name ||
			!strings.HasPrefix(p.Name, "katzenpost-"+strings.TrimPrefix(o.Tag, "v")+"+") ||
			!strings.HasSuffix(p.Name, "-"+token+".tar.gz") {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		tmp, err := os.CreateTemp(dir, ".bundle-*.tar.gz")
		if err != nil {
			return "", err
		}
		tmp.Close()
		candidate := tmp.Name()
		defer os.Remove(candidate)
		prefix := strings.TrimSuffix(p.Name, ".tar.gz")
		if err := release.Tarball(bindir, prefix, candidate, epoch, configs...); err != nil {
			return "", err
		}
		if err := normalizeArchiveCompression(dir, []string{candidate}, o.Pins); err != nil {
			return "", err
		}
		sum, _, err := fsutil.SHA256File(candidate)
		if err != nil {
			return "", err
		}
		if sum != p.Sha256 {
			continue
		}
		dst := filepath.Join(dir, p.Name)
		if err := os.Rename(candidate, dst); err != nil {
			return "", err
		}
		note("%s matches the recorded bundle; keeping its name", p.Name)
		return dst, nil
	}
	return "", nil
}
