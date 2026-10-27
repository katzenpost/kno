package build

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/release"
)

type tarballOpts struct {
	Repo       string
	Network    string
	Tag        string
	Work       string
	Rel        string
	Platforms  []compile.Platform
	FreeBSDABI string

	Source  bool
	Pins    map[string]string
	ToolVer string
}

func tarballs(o tarballOpts) ([]string, error) {
	epoch, err := deb.Epoch(o.Repo, o.Tag)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(o.Rel, "pkgs")
	ver := strings.TrimPrefix(o.Tag, "v")

	configs := clientConfigs(o.Rel)

	var out []string
	add := func(bindir, token string) error {
		if !hasFiles(bindir) {
			return nil
		}
		name := fmt.Sprintf("katzenpost-%s-%s", ver, token)
		dst := filepath.Join(dir, name+".tar.gz")
		if err := release.Tarball(bindir, name, dst, epoch, configs...); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		out = append(out, dst)
		return nil
	}

	for _, p := range o.Platforms {
		if err := add(binDir(o.Work, p), compile.OSToken(p.OS)+"-"+p.Arch); err != nil {
			return nil, err
		}
	}
	if o.FreeBSDABI != "" {
		if err := add(filepath.Join(o.Work, "fbsd", "bin"), "freebsd-"+abiArch(o.FreeBSDABI)); err != nil {
			return nil, err
		}
	}
	if o.Source {
		srcs, err := sourceTarballs(o.Repo, o.Work, dir, ver, o.ToolVer,
			o.Pins, epoch)
		if err != nil {
			return nil, err
		}
		out = append(out, srcs...)
	}
	return out, nil
}

func sourceTarballs(repo, work, dir, ver, toolVer string,
	pins map[string]string, epoch int64) ([]string, error) {
	src := filepath.Join(work, ".src")
	var out []string
	name := "katzenpost-" + ver
	for _, p := range []struct{ name, repo, ref string }{
		{name, "katzenpost", pins["KATZENPOST_SHA"]},
		{name + "-hpqc", "hpqc", pins["HPQC_SHA"]},
	} {
		dst := filepath.Join(dir, p.name+"-src.tar.gz")
		err := release.SourceTarball(filepath.Join(src, p.repo), p.ref, p.name,
			dst, epoch)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(dst), err)
		}
		out = append(out, dst)
	}
	root, ok := config.GitRoot(repo)
	if !ok {
		note("no kno source tarball: %s is not a git checkout", repo)
		return out, nil
	}
	name = "kno-" + toolVer
	kn := filepath.Join(dir, name+"-src.tar.gz")
	if err := release.SourceTarball(root, "HEAD", name, kn, epoch); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(kn), err)
	}
	return append(out, kn), nil
}

func hasFiles(dir string) bool {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range ents {
		if !e.IsDir() {
			return true
		}
	}
	return false
}

func abiArch(abi string) string {
	if i := strings.LastIndex(abi, ":"); i >= 0 {
		return abi[i+1:]
	}
	return abi
}

func clientConfigs(rel string) []release.TarballFile {
	want := []release.TarballFile{
		{Name: "etc/katzenpost/client.toml", Path: filepath.Join(rel, "client.toml")},
		{Name: "etc/katzenpost/thinclient.toml", Path: filepath.Join(rel, "thinclient.toml")},
	}
	var out []release.TarballFile
	for _, f := range want {
		if fi, err := os.Stat(f.Path); err == nil && fi.Mode().IsRegular() {
			out = append(out, f)
		}
	}
	return out
}
