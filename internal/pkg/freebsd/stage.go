package freebsd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/pkg/deb"
)

const DefaultABI = "FreeBSD:14:amd64"

const (
	pkgName    = "katzenpost"
	pkgOrigin  = "net/katzenpost"
	pkgComment = "Katzenpost pq mixnet binaries"
	pkgWww     = "https://github.com/katzenpost/katzenpost"
	pkgPrefix  = "/usr/local"
	pkgDesc    = "Every katzenpost command, named as in the Debian package, an rc.d service that runs one of them under daemon(8) -r, and the default kpclientd client config. Network-generic; per-node config is deployed separately."
)

var daemons = map[string]bool{"dirauth": true, "server": true, "replica": true, "courier": true}

func InstallName(cmd string) string {
	if strings.HasPrefix(cmd, "kp") {
		return cmd
	}
	return "kp" + cmd
}

func InstallPath(cmd string) string {
	if daemons[cmd] {
		return "usr/local/sbin/" + InstallName(cmd)
	}
	return "usr/local/bin/" + InstallName(cmd)
}

func SplitPkgName(base string) (string, bool) {
	stem, ok := strings.CutSuffix(base, ".pkg")
	i := strings.LastIndexByte(stem, '-')
	if !ok || i <= 0 {
		return "", false
	}
	return stem[:i], true
}

func PkgAdd(root string, pkgs ...string) []string {
	argv := []string{"env", "ASSUME_ALWAYS_YES=yes", "pkg"}
	if root != "" {
		argv = append(argv, "-c", root)
	}
	return append(append(argv, "add", "-f"), pkgs...)
}

func PkgAddInRoot(root string, pkgs ...string) []string {
	return append([]string{"sh", "-c",
		`abi=$(pkg config abi) && exec env ASSUME_ALWAYS_YES=yes IGNORE_OSVERSION=yes pkg -o ABI="$abi" -r "$0" add -f -M "$@"`,
		root}, pkgs...)
}

func PkgQueryInRoot(root string, args ...string) []string {
	return append([]string{"sh", "-c", `exec pkg -o ABI="$(pkg config abi)" -r "$0" query "$@"`, root}, args...)
}

func OnePkg(dir, name string) (string, error) {
	matches, _ := filepath.Glob(filepath.Join(dir, name+"-*.pkg"))
	var out []string
	for _, m := range matches {
		if n, ok := SplitPkgName(filepath.Base(m)); ok && n == name {
			out = append(out, m)
		}
	}
	switch len(out) {
	case 0:
		return "", fmt.Errorf("missing package: no %s-<version>.pkg in %s", name, dir)
	case 1:
		return out[0], nil
	}
	return "", fmt.Errorf("%d versions of %s in %s; a release holds one", len(out), name, dir)
}

func Commands(src string) ([]string, error) {
	ents, err := os.ReadDir(filepath.Join(src, "cmd"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() && isMain(filepath.Join(src, "cmd", e.Name())) {
			out = append(out, e.Name())
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no main package under %s/cmd", src)
	}
	return out, nil
}

var mainPkg = regexp.MustCompile(`(?m)^package main\b`)

func isMain(dir string) bool {
	files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err == nil && mainPkg.Match(b) {
			return true
		}
	}
	return false
}

func Ldflags(tag string) string {
	return "-s -w -buildid= -linkmode=internal -X github.com/katzenpost/katzenpost/common.BuildVersion=" +
		strings.TrimPrefix(tag, "v")
}

func built(bindir string) ([]string, error) {
	ents, err := os.ReadDir(bindir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if e.Type().IsRegular() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

func Stage(bindir, rel, repo string) (string, error) {
	if !fsutil.IsDir(bindir) {
		return "", fmt.Errorf("bin dir missing: %s", bindir)
	}
	root := filepath.Join(rel, ".pkgbuild-freebsd")
	if err := os.RemoveAll(root); err != nil {
		return "", err
	}
	for _, d := range []string{
		"usr/local/sbin", "usr/local/bin",
		"usr/local/etc/rc.d", "usr/local/etc/kpclientd",
	} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			return "", err
		}
	}

	names, err := built(bindir)
	if err != nil {
		return "", err
	}
	if len(names) == 0 {
		return "", fmt.Errorf("no katzenpost binaries found in %s", bindir)
	}
	for _, n := range names {
		if err := install(filepath.Join(bindir, n), filepath.Join(root, InstallPath(n)), 0o755); err != nil {
			return "", err
		}
	}

	rcd := os.Getenv("FREEBSD_RCD")
	if rcd == "" {
		rcd = filepath.Join(repo, "packaging", "freebsd", "rc.d", "katzenpost")
	}
	for _, script := range []string{rcd, filepath.Join(filepath.Dir(rcd), "kpclientd")} {
		if readable(script) {
			if err := install(script, filepath.Join(root, "usr/local/etc/rc.d", filepath.Base(script)), 0o755); err != nil {
				return "", err
			}
		}
	}
	for _, f := range []string{"client.toml", "thinclient.toml"} {
		b, err := os.ReadFile(filepath.Join(rel, f))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		dst := filepath.Join(root, "usr/local/etc/kpclientd", f)
		if err := os.WriteFile(dst, ClientSocket(b), 0o644); err != nil {
			return "", err
		}
	}
	return root, nil
}

const ClientSocketPath = "/var/run/kpclientd/katzenpost.sock"

var abstractSocket = regexp.MustCompile(`(?m)^(\s*Address\s*=\s*)"@[^"]*"`)

func ClientSocket(toml []byte) []byte {
	return abstractSocket.ReplaceAll(toml, []byte(`${1}"`+ClientSocketPath+`"`))
}

func StageBin(bindir, rel string) error {
	dst := filepath.Join(rel, "bin")
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	names, err := built(bindir)
	if err != nil {
		return err
	}
	for _, n := range names {
		if err := install(filepath.Join(bindir, n), filepath.Join(dst, n), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func Package(bindir, rel, tag, rev, repo, abi string) (string, error) {
	return PackageStamped(bindir, rel, tag, rev, repo, abi, "")
}

func PackageStamped(bindir, rel, tag, rev, repo, abi, stamp string) (string, error) {
	if abi == "" {
		abi = DefaultABI
	}
	epoch, err := deb.Epoch(repo, tag)
	if err != nil {
		return "", err
	}
	root, err := Stage(bindir, rel, repo)
	if err != nil {
		return "", err
	}

	if err := StageBin(bindir, rel); err != nil {
		return "", fmt.Errorf("stage release bin: %w", err)
	}
	pkgs := filepath.Join(rel, "pkgs")
	if err := os.MkdirAll(pkgs, 0o755); err != nil {
		return "", err
	}

	version := Version(deb.Version(tag, rev) + stamp)
	out := filepath.Join(pkgs, pkgName+"-"+version+".pkg")
	meta := Meta{
		Name: pkgName, Version: version, Origin: pkgOrigin, Abi: abi,
		Comment: pkgComment, Maintainer: Maintainer(), Www: pkgWww,
		Prefix: pkgPrefix, Desc: pkgDesc,
	}
	if err := Write(root, out, meta, epoch); err != nil {
		return "", err
	}
	return out, nil
}

func Maintainer() string {
	if m := os.Getenv("PKG_MAINTAINER"); m != "" {
		return m
	}
	return "packages@katzenpost.network"
}

func install(src, dst string, mode os.FileMode) error {
	if err := fsutil.CopyFileMode(src, dst, mode); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}

func readable(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	f.Close()
	return true
}
