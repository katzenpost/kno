package deb

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/katzenpost/kno/internal/argcheck"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/service"
)

func parseManifest(path string) (service.Service, error) {
	f, err := os.Open(path)
	if err != nil {
		return service.Service{}, err
	}
	defer f.Close()
	m := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		m[strings.TrimSpace(k)] = strings.Trim(v, `"`)
	}
	if err := sc.Err(); err != nil {
		return service.Service{}, err
	}
	s := service.Service{
		Backend: m["BACKEND"], Unit: m["UNIT"], Build: m["BUILD"],
		BinarySrc: m["BINARY_SRC"], BinaryDst: m["BINARY_DST"],
		ConfigSrc: m["CONFIG_SRC"], ConfigDst: m["CONFIG_DST"],
		StateDir: m["STATE_DIR"], SecretKeys: m["SECRET_KEYS"],
		Jail: m["JAIL"], PrestartHook: m["PRESTART_HOOK"],
		ServiceUser: m["SERVICE_USER"],
		LegacyUnit:  m["LEGACY_UNIT"], LegacyStateDir: m["LEGACY_STATE_DIR"],
		LegacyKeep: strings.Fields(m["LEGACY_KEEP"]),
		ExtraMoves: strings.Fields(m["EXTRA_MOVES"]),
	}
	if v := m["KEYS_SRC"]; v != "" {
		s.KeysSrc = strings.Fields(v)
	}
	if v := m["AUX_FILES"]; v != "" {
		s.AuxFiles = strings.Fields(v)
	}
	return s, nil
}

func Maintainer() string { return EnvSettings().maintainer() }

func debVersion(tag string) string { return strings.TrimPrefix(tag, "v") }

func SplitRevision(tag string) (ref, rev string) { return knopath.SplitRevision(tag) }

func Version(tag, rev string) string {
	v := debVersion(tag)
	if rev != "" {
		v += "-" + rev
	}
	return v
}

func writeExec(path, content string) error {
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		return err
	}
	return nil
}

func AssembleConfigRoot(rel, tag, rev, svc, rootParent, arch string) (string, error) {
	return AssembleConfigRootFor(rel, tag, rev, svc, rootParent, arch, "")
}

func AssembleConfigRootFor(rel, tag, rev, svc, rootParent, arch, osName string) (string, error) {
	return AssembleConfig(ConfigRoot{
		Rel: rel, Tag: tag, Rev: rev, BinRev: rev, Svc: svc,
		RootParent: rootParent, Arch: arch, OS: osName,
	})
}

type ConfigRoot struct {
	Rel, Tag, Svc, RootParent, Arch, OS string
	Rev, BinRev, Stamp                  string
}

func AssembleConfig(c ConfigRoot) (string, error) {
	rel, tag, rev, svc := c.Rel, c.Tag, c.Rev, c.Svc
	rootParent, arch, osName := c.RootParent, c.Arch, c.OS
	if arch == "" {
		arch = ArchAll
	}
	s, err := parseManifest(filepath.Join(rel, "manifest", svc+".conf"))
	if err == nil {

		err = CheckServiceValues(s)
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w", svc, err)
	}
	for name, v := range map[string]string{
		"CONFIG_DST": s.ConfigDst, "STATE_DIR": s.StateDir,
		"UNIT": s.Unit, "BINARY_DST": s.BinaryDst, "SERVICE_USER": s.ServiceUser,
	} {
		if v == "" {
			return "", fmt.Errorf("%s: no %s", svc, name)
		}
	}
	base := knopath.ConfigPkgPrefix + svc
	pkg := base
	sibling := Flavoured(base, true)
	root := filepath.Join(rootParent, pkg)
	if err := os.RemoveAll(root); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(root, "DEBIAN"), 0o755); err != nil {
		return "", err
	}

	cfgSrc := filepath.Join(rel, "configs", filepath.Base(s.ConfigSrc))
	if _, err := os.Stat(cfgSrc); err != nil {
		return "", fmt.Errorf("%s: staged config missing: %s", svc, cfgSrc)
	}
	if err := fsutil.CopyFile(cfgSrc, root+s.ConfigDst); err != nil {
		return "", err
	}

	for _, af := range s.AuxFiles {
		src, dst, ok := strings.Cut(af, "=")
		if !ok {
			continue
		}
		asrc := filepath.Join(rel, "configs", src)
		if _, err := os.Stat(asrc); err != nil {
			return "", fmt.Errorf("%s: aux config missing: %s", svc, asrc)
		}
		if err := fsutil.CopyFile(asrc, root+dst); err != nil {
			return "", err
		}
	}

	for _, kd := range s.KeysSrc {
		sd := filepath.Join(rel, "keys", filepath.Base(kd))
		if !fsutil.IsDir(sd) {
			continue
		}
		if err := fsutil.CopyTreeGuarded(sd, root+s.StateDir); err != nil {
			return "", err
		}
	}

	if secret, _ := fsutil.FindSecret(root); secret != "" {
		return "", fmt.Errorf("%s: refusing to package a private key (%s)", svc, secret)
	}
	if s.Backend == "" || s.Backend == "systemd" {
		return finishUpstreamConfig(c, s, root, pkg)
	}

	unit := fmt.Sprintf(`[Unit]
Description=namenlos %s
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
IPAccounting=yes
User=%s
WorkingDirectory=%s
ExecStartPre=%s -f %s --validate-only
ExecStart=%s -f %s
PrivateTmp=yes
NoNewPrivileges=yes
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
Restart=always
RestartSec=10s

[Install]
WantedBy=default.target
`, svc, s.ServiceUser, s.StateDir, s.BinaryDst, s.ConfigDst, s.BinaryDst, s.ConfigDst)
	if err := os.MkdirAll(filepath.Join(root, "lib/systemd/system"), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(root, "lib/systemd/system", s.Unit+".service"), []byte(unit), 0o644); err != nil {
		return "", err
	}

	debver := configVersion(tag, rev, c.Stamp)
	binver := OSVersion(tag, c.BinRev, osName)

	control := fmt.Sprintf(`Package: %s
Version: %s
Architecture: %s
Maintainer: %s
Depends: %s (= %s)
%sSection: net
Priority: optional
Description: namenlos configuration for %s (%s)
 Rendered configuration, public keys and the systemd unit for the namenlos
 service %s. Diverts the katzenpost default config, ships the %s unit and
 enables it. Requires the matching katzenpost version (%s).
`, pkg, debver, arch, Maintainer(),
		"katzenpost", binver,
		AlternativesFields(base, debver), svc, tag, svc, s.Unit, binver)
	if err := os.WriteFile(filepath.Join(root, "DEBIAN/control"), []byte(control), 0o644); err != nil {
		return "", err
	}

	div := s.ConfigDst + ".default"
	hook := ""
	if s.PrestartHook != "" {
		hook = s.PrestartHook + " || true"
	}

	takeover := fmt.Sprintf(
		"if [ \"$(dpkg-divert --listpackage %s 2>/dev/null)\" = %s ]; then\n"+
			"\tdpkg-divert --remove --rename --package %s --divert %s %s\n"+
			"fi\n",
		s.ConfigDst, sibling, sibling, div, s.ConfigDst)
	if err := writeExec(filepath.Join(root, "DEBIAN/preinst"), fmt.Sprintf(
		"#!/bin/sh\nset -e\n%sdpkg-divert --add --rename --package %s --divert %s %s\n",
		takeover, pkg, div, s.ConfigDst)); err != nil {
		return "", err
	}

	postinst := fmt.Sprintf(`#!/bin/sh
set -e
if [ "$1" = configure ]; then
	if ! getent group '%[1]s' >/dev/null 2>&1; then
		groupadd --system '%[1]s'
	fi
	if ! getent passwd '%[1]s' >/dev/null 2>&1; then
		useradd --system --gid '%[1]s' --home-dir '%[2]s' \
			--no-create-home --shell /usr/sbin/nologin '%[1]s'
	fi
	mkdir -p '%[2]s'
	chown -R '%[1]s':'%[1]s' '%[2]s'
	chmod 0700 '%[2]s'
	if [ -f '%[3]s' ]; then
		chown root:'%[1]s' '%[3]s'
		chmod 0640 '%[3]s'
	fi
fi
if [ "$1" = configure ] && command -v systemctl >/dev/null 2>&1; then
	systemctl daemon-reload || true
	# A unit that crash-looped is left failed, and past StartLimitBurst systemd
	# refuses to start it again at all: "start request repeated too quickly".
	# Installing the release that fixes the crash would then change nothing,
	# because nothing would start it. Clear the counter first.
	systemctl reset-failed '%[5]s.service' || true
	%[4]s
	systemctl enable --now '%[5]s.service' || true
fi
`, s.ServiceUser, s.StateDir, s.ConfigDst, hook, s.Unit)
	if err := writeExec(filepath.Join(root, "DEBIAN/postinst"), postinst); err != nil {
		return "", err
	}
	if err := writeExec(filepath.Join(root, "DEBIAN/prerm"), fmt.Sprintf(
		"#!/bin/sh\nset -e\nif [ \"$1\" = remove ] && command -v systemctl >/dev/null 2>&1; then\n\tsystemctl stop %[1]s.service 2>/dev/null || true\n\tsystemctl disable %[1]s.service 2>/dev/null || true\nfi\n",
		s.Unit)); err != nil {
		return "", err
	}
	if err := writeExec(filepath.Join(root, "DEBIAN/postrm"), fmt.Sprintf(
		"#!/bin/sh\nset -e\nif [ \"$1\" = purge ]; then\n\tdpkg-divert --remove --rename --package %s --divert %s %s || true\nfi\n",
		pkg, div, s.ConfigDst)); err != nil {
		return "", err
	}
	return root, nil
}

func RootContentID(root string) (string, error) {
	h := sha256.New()
	var names []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		names = append(names, rel)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(names)
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(root, n))
		if err != nil {
			return "", err
		}
		if n == filepath.Join("DEBIAN", "control") {
			b = dropVersionLine(b)
		}
		fmt.Fprintf(h, "%s\n%d\n", n, len(b))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:32], nil
}

func dropVersionLine(b []byte) []byte {
	lines := strings.Split(string(b), "\n")
	var version string
	for _, line := range lines {
		if v, ok := strings.CutPrefix(line, "Version:"); ok {
			version = strings.TrimSpace(v)
			break
		}
	}
	var out []string
	for _, line := range lines {
		if strings.HasPrefix(line, "Version:") {
			continue
		}
		if version != "" && strings.HasPrefix(line, "Provides:") {
			line = strings.ReplaceAll(line, "(= "+version+")", "(= <own>)")
		}
		out = append(out, line)
	}
	return []byte(strings.Join(out, "\n"))
}

func CheckServiceValues(s service.Service) error {
	if err := argcheck.User("SERVICE_USER", s.ServiceUser); err != nil {
		return err
	}
	if err := argcheck.Path("STATE_DIR", s.StateDir); err != nil {
		return err
	}
	if err := argcheck.Path("CONFIG_DST", s.ConfigDst); err != nil {
		return err
	}
	return argcheck.Arg("UNIT", s.Unit)
}
