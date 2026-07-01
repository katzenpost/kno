package build

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/katzenpost/kno/internal/compile"
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

	Source bool
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
		src, err := sourceTarball(o.Repo, o.Network, o.Tag, ver, dir, epoch)
		if err != nil {
			return nil, err
		}
		if src != "" {
			out = append(out, src)
		}
	}
	return out, nil
}

func sourceTarball(repo, network, tag, ver, dir string, epoch int64) (string, error) {
	ref := resolveTag(repo, network, tag)
	if ref == "" {
		note("no kno source tarball: this kno repository has no git tag %s or %s/%s"+
			" (the katzenpost tag is not affected)", tag, network, tag)
		return "", nil
	}
	name := "kno-" + ver
	dst := filepath.Join(dir, name+"-src.tar.gz")
	if err := release.SourceTarball(repo, ref, name, dst, epoch); err != nil {
		return "", err
	}
	return dst, nil
}

func resolveTag(repo, network, tag string) string {
	refs := []string{"refs/tags/" + tag}
	if network != "" {
		refs = append(refs, "refs/tags/"+network+"/"+tag)
	}
	for _, ref := range refs {
		if exec.Command("git", "-C", repo, "rev-parse", "--verify", "--quiet",
			ref+"^{commit}").Run() == nil {
			return ref
		}
	}
	return ""
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
