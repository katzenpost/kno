package freebsd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/service"

	"github.com/katzenpost/kno/internal/knopath"
)

func ConfigPkgName(svc string) string { return knopath.ConfigPkgPrefix + svc }

func StageConfig(rel, svc string, s Svc, rootParent string) (string, error) {
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
		if err := fsutil.CopyTreeGuarded(sd, filepath.Join(root, strings.TrimPrefix(KeysDir(svc), "/"))); err != nil {
			return "", fmt.Errorf("%s: %w", svc, err)
		}
	}
	if secret, _ := fsutil.FindSecret(root); secret != "" {
		return "", fmt.Errorf("%s: refusing to package a private key (%s)", svc, secret)
	}

	return root, nil
}

func postInstall(stateDir string) string {
	return "#!/bin/sh\nset -e\nd=\"${PKG_ROOTDIR:-}" + stateDir + "\"\n[ -d \"$d\" ] || install -d -m 0700 \"$d\"\n"
}

type Svc struct {
	Build     string
	ConfigSrc string
	ConfigDst string
	StateDir  string
	KeysSrc   []string
	Stamp     string
}

func SvcOf(rs service.Service, stamp string) Svc {
	return Svc{
		Build:     rs.Build,
		ConfigSrc: rs.ConfigSrc,
		ConfigDst: rs.ConfigDst,
		StateDir:  rs.StateDir,
		KeysSrc:   rs.KeysSrc,
		Stamp:     stamp,
	}
}

func PackageConfig(rel, tag, rev, svc string, s Svc, repo, abi string, version func(content, want string) string) (string, error) {
	root, err := StageConfig(rel, svc, s, filepath.Join(rel, ".pkgbuild-freebsd-cfg"))
	if err != nil {
		return "", err
	}
	kp := Version(deb.Version(tag, rev))
	post := postInstall(s.StateDir)
	ver := Version(deb.Version(tag, rev) + s.Stamp)
	if version != nil {
		id, err := deb.RootContentID(root)
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256([]byte(id + "\n" + kp + "\n" + post))
		ver = version(hex.EncodeToString(sum[:])[:32], ver)
	}
	meta := Meta{
		Name:    ConfigPkgName(svc),
		Version: ver,
		Origin:  "net/" + ConfigPkgName(svc),
		Abi:     abi,
		Comment: "namenlos configuration for " + svc,
		Www:     "https://katzenpost.network",
		Desc: "Rendered configuration and public keys of the namenlos service " + svc + ".\n" +
			"Requires the matching katzenpost " + kp + ".",
		Deps: map[string]depEntry{
			"katzenpost": Dep("net/katzenpost", kp),
		},
		Scripts: map[string]string{"post-install": post},
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
