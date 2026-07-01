package freebsd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/service"

	"github.com/katzenpost/kno/internal/knopath"
)

func binaryFor(build string) (string, error) {
	if build == "" {
		return "", fmt.Errorf("no build target")
	}
	return "/" + InstallPath(build), nil
}

func ConfigPkgName(svc string) string { return knopath.ConfigPkgPrefix + svc }

func StageConfig(rel, svc string, s Svc, rootParent string) (string, error) {
	bin, err := binaryFor(s.Build)
	if err != nil {
		return "", fmt.Errorf("%s: %w", svc, err)
	}
	if s.ConfigDst == "" || s.StateDir == "" {
		return "", fmt.Errorf("%s: needs CONFIG_DST and STATE_DIR", svc)
	}
	root := filepath.Join(rootParent, ConfigPkgName(svc))
	if err := os.RemoveAll(root); err != nil {
		return "", err
	}

	cfgSrc := filepath.Join(rel, "configs", filepath.Base(s.ConfigSrc))
	if _, err := os.Stat(cfgSrc); err != nil {
		return "", fmt.Errorf("%s: staged config missing: %s", svc, cfgSrc)
	}
	if err := fsutil.CopyFile(cfgSrc, filepath.Join(root, strings.TrimPrefix(s.ConfigDst, "/"))); err != nil {
		return "", err
	}

	for _, kd := range s.KeysSrc {
		sd := filepath.Join(rel, "keys", filepath.Base(kd))
		if !fsutil.IsDir(sd) {
			continue
		}
		if err := fsutil.CopyTreeGuarded(sd, filepath.Join(root, strings.TrimPrefix(s.StateDir, "/"))); err != nil {
			return "", fmt.Errorf("%s: %w", svc, err)
		}
	}
	if secret, _ := fsutil.FindSecret(root); secret != "" {
		return "", fmt.Errorf("%s: refusing to package a private key (%s)", svc, secret)
	}

	var rc strings.Builder
	fmt.Fprintf(&rc, "# Written by %s. One jail, one service.\n", ConfigPkgName(svc))
	fmt.Fprintf(&rc, "katzenpost_enable=\"YES\"\n")
	fmt.Fprintf(&rc, "katzenpost_program=\"\"\n")
	fmt.Fprintf(&rc, "katzenpost_daemon=\"%s\"\n", bin)
	fmt.Fprintf(&rc, "katzenpost_config=\"%s\"\n", s.ConfigDst)
	if s.PrestartHook != "" {
		fmt.Fprintf(&rc, "katzenpost_prestart=\"%s\"\n", s.PrestartHook)
	}
	rcPath := filepath.Join(root, "usr/local/etc/rc.conf.d/katzenpost")
	if err := os.MkdirAll(filepath.Dir(rcPath), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(rcPath, []byte(rc.String()), 0o644); err != nil {
		return "", err
	}

	return root, nil
}

func postInstall(stateDir string) string {
	return "#!/bin/sh\nset -e\nd=\"${PKG_ROOTDIR:-}" + stateDir + "\"\n[ -d \"$d\" ] || install -d -m 0700 \"$d\"\n"
}

type Svc struct {
	Build        string
	ConfigSrc    string
	ConfigDst    string
	StateDir     string
	KeysSrc      []string
	PrestartHook string
	Stamp        string
}

func SvcOf(rs service.Service, stamp string) Svc {
	return Svc{
		Build:        rs.Build,
		ConfigSrc:    rs.ConfigSrc,
		ConfigDst:    rs.ConfigDst,
		StateDir:     rs.StateDir,
		KeysSrc:      rs.KeysSrc,
		PrestartHook: rs.PrestartHook,
		Stamp:        stamp,
	}
}

func PackageConfig(rel, tag, rev, svc string, s Svc, repo, abi string) (string, error) {
	root, err := StageConfig(rel, svc, s, filepath.Join(rel, ".pkgbuild-freebsd-cfg"))
	if err != nil {
		return "", err
	}
	kp := Version(deb.Version(tag, rev))
	meta := Meta{
		Name:    ConfigPkgName(svc),
		Version: Version(deb.Version(tag, rev) + s.Stamp),
		Origin:  "net/" + ConfigPkgName(svc),
		Abi:     abi,
		Comment: "namenlos configuration for " + svc,
		Www:     "https://katzenpost.network",
		Desc: "Rendered configuration, public keys and the rc.conf.d settings for\n" +
			"the namenlos service " + svc + ". Requires the matching katzenpost " + kp + ".",
		Deps: map[string]depEntry{
			"katzenpost": Dep("net/katzenpost", kp),
		},
		Scripts: map[string]string{"post-install": postInstall(s.StateDir)},
	}
	epoch, err := deb.Epoch(repo, tag)
	if err != nil {
		return "", err
	}
	out, err := writePkg(root, filepath.Join(rel, "pkgs"), meta, epoch)
	if err != nil {
		return "", err
	}
	os.RemoveAll(filepath.Join(rel, ".pkgbuild-freebsd-cfg"))
	return out, nil
}

const ClientService = "kpclientd"

func PackageClient(rel, tag, rev, repo, abi string) (string, error) {
	root := filepath.Join(rel, ".pkgbuild-freebsd-client")
	if err := os.RemoveAll(root); err != nil {
		return "", err
	}
	rc := filepath.Join(root, "usr/local/etc/rc.conf.d/kpclientd")
	if err := os.MkdirAll(filepath.Dir(rc), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(rc, []byte("kpclientd_enable=\"YES\"\n"), 0o644); err != nil {
		return "", err
	}
	ver := Version(deb.Version(tag, rev))
	meta := Meta{
		Name:    ConfigPkgName(ClientService),
		Version: ver,
		Origin:  "net/" + ConfigPkgName(ClientService),
		Abi:     abi,
		Comment: "namenlos client daemon for kno verify",
		Www:     "https://katzenpost.network",
		Desc: "Enables kpclientd in the jail that kno verify asks for the consensus.\n" +
			"The client config comes with katzenpost " + ver + ".",
		Deps: map[string]depEntry{"katzenpost": Dep("net/katzenpost", ver)},
	}
	epoch, err := deb.Epoch(repo, tag)
	if err != nil {
		return "", err
	}
	return writePkg(root, filepath.Join(rel, "pkgs"), meta, epoch)
}
