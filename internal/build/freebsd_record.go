package build

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/pkg/freebsd"
)

func freebsdPackage(o Options, bindir, rel, abi string) (string, error) {
	if o.ToolRevision == "" {
		entries, err := committedEntries(o.Repo, o.Network, o.Tag, o.kind())
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name > entries[j].Name })
		for _, p := range entries {
			if n, ok := freebsd.SplitPkgName(p.Name); !ok || n != "katzenpost" || filepath.Base(p.Name) != p.Name {
				continue
			}
			ver := strings.TrimSuffix(strings.TrimPrefix(p.Name, "katzenpost-"), ".pkg")
			stamp := "+" + strings.TrimSuffix(strings.TrimPrefix(ver, version(o.Tag)+"."), "_"+o.Revision)
			if freebsd.Version(deb.Version(o.Tag, o.Revision)+stamp) != ver {
				continue
			}
			tmp, err := os.MkdirTemp(rel, ".freebsd-record-")
			if err != nil {
				return "", err
			}
			candidate, err := recordedFreeBSDCandidate(o, bindir, rel, tmp, abi, stamp)
			if err != nil {
				os.RemoveAll(tmp)
				return "", err
			}
			sum, _, err := fsutil.SHA256File(candidate)
			if err != nil {
				os.RemoveAll(tmp)
				return "", err
			}
			if sum != p.Sha256 {
				os.RemoveAll(tmp)
				continue
			}
			dst := filepath.Join(rel, "pkgs", p.Name)
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				os.RemoveAll(tmp)
				return "", err
			}
			if err := os.Rename(candidate, dst); err != nil {
				os.RemoveAll(tmp)
				return "", err
			}
			os.RemoveAll(tmp)
			if err := freebsd.StageBin(bindir, rel); err != nil {
				return "", err
			}
			note("%s matches the recorded package; keeping its version", p.Name)
			return dst, nil
		}
	}
	return freebsd.PackageStamped(bindir, rel, o.Tag, o.Revision, o.Repo, abi, deb.ConfigStamp(o.Repo, o.Network, "client"))
}

func recordedFreeBSDCandidate(o Options, bindir, rel, tmp, abi, stamp string) (string, error) {
	for _, name := range []string{"client.toml", "thinclient.toml"} {
		src := filepath.Join(rel, name)
		if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return "", err
		}
		if err := fsutil.CopyFile(src, filepath.Join(tmp, name)); err != nil {
			return "", err
		}
	}
	return freebsd.PackageStamped(bindir, tmp, o.Tag, o.Revision, o.Repo, abi, stamp)
}

func freebsdBinaryStamp(o Options, p string) string {
	if p != "" {
		ver := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(p), "katzenpost-"), ".pkg")
		stamp := "+" + strings.TrimSuffix(strings.TrimPrefix(ver, version(o.Tag)+"."), "_"+o.Revision)
		if freebsd.Version(deb.Version(o.Tag, o.Revision)+stamp) == ver {
			return stamp
		}
	}
	return deb.ConfigStamp(o.Repo, o.Network, "client")
}
