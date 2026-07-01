package deb

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/katzenpost/kno/internal/service"
)

const upstreamLayoutSince = "0.0.107"

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func shList(xs []string) string { return shq(strings.Join(xs, " ")) }

func dropIn(s service.Service) string {
	return fmt.Sprintf(`[Unit]
RequiresMountsFor=%s

[Service]
ExecStartPre=%s -f %s --validate-only
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
LogsDirectory=katzenpost
`, s.StateDir, s.BinaryDst, s.ConfigDst)
}

func dropInPath(s service.Service) string {
	return "/usr/lib/systemd/system/" + s.Unit + ".service.d/kno.conf"
}

const scriptHead = `#!/bin/sh
set -e
R="${DPKG_ROOT:-}"
live() { [ -z "$R" ] && command -v systemctl >/dev/null 2>&1; }
`

func upstreamPreinst(pkg string, s service.Service) string {
	var b strings.Builder
	b.WriteString(scriptHead)
	fmt.Fprintf(&b, `if [ "$1" = install ] || [ "$1" = upgrade ]; then
	if [ -z "$R" ] && command -v dpkg-divert >/dev/null 2>&1; then
		dpkg-divert --list 2>/dev/null | awk -v p=%[1]s '$NF == p { print $3, $5 }' |
		while read -r path to; do
			dpkg-divert --remove --no-rename --package %[1]s --divert "$to" "$path"
		done
	fi
`, shq(pkg))
	if s.LegacyUnit != "" {
		fmt.Fprintf(&b, `	if live; then
		systemctl stop %[1]s 2>/dev/null || true
		systemctl disable %[1]s 2>/dev/null || true
	fi
`, shq(s.LegacyUnit+".service"))
	}
	b.WriteString("fi\n")
	return b.String()
}

func upstreamPostinst(pkg string, s service.Service, configs []string) string {
	var b strings.Builder
	b.WriteString(scriptHead)
	fmt.Fprintf(&b, `old=%s
new=%s
keep=%s
moves=%s
user=%s
`, shq(s.LegacyStateDir), shq(s.StateDir), shList(s.LegacyKeep), shList(s.ExtraMoves), shq(s.ServiceUser))
	fmt.Fprintf(&b, `samefs() {
	if [ "$(stat -c %%d "$1")" != "$(stat -c %%d "$2")" ]; then
		echo "%[1]s: $1 and $2 are on different filesystems." >&2
		echo "  kno does not move node state to another filesystem. Mount $2 on the filesystem of $1, then run:" >&2
		echo "  dpkg --configure %[1]s" >&2
		exit 1
	fi
}
if [ "$1" = configure ]; then
	mkdir -p "$R$new"
	if [ -n "$old" ] && [ -d "$R$old" ] && [ "$old" != "$new" ] && [ ! -e "$R$new/identity.private.pem" ]; then
		samefs "$R$old" "$R$new"
		for e in "$R$old"/* "$R$old"/.[!.]* "$R$old"/..?*; do
			[ -e "$e" ] || [ -L "$e" ] || continue
			skip=
			for k in $keep "$new"; do
				case "$R$k" in "$e" | "$e"/*) skip=1 ;; esac
			done
			n=$(basename "$e")
			case "$n" in .kno-migrated*) skip=1 ;; esac
			[ -z "$skip" ] && [ ! -e "$R$new/$n" ] || continue
			mv "$e" "$R$new/$n"
			printf '%%s\n' "$n" >> "$R$new/.kno-migrated"
		done
		printf '%%s\n' "$old" > "$R$new/.kno-migrated-from"
	fi
	for m in $moves; do
		from=${m%%%%=*}
		to=${m#*=}
		if [ -d "$R$from" ] && [ ! -e "$R$to" ]; then
			mkdir -p "$(dirname "$R$to")"
			samefs "$R$from" "$(dirname "$R$to")"
			mv "$R$from" "$R$to"
			printf '%%s\n' "$m" >> "$R$new/.kno-migrated-moves"
		fi
		if [ -d "$R$to" ]; then
			chown -R "$user:$user" "$R$to"
		fi
	done
	chown -R "$user:$user" "$R$new"
	chmod 0700 "$R$new"
`, pkg)
	for _, c := range configs {
		fmt.Fprintf(&b, `	if [ -f "$R"%[1]s ]; then
		chown "root:$user" "$R"%[1]s
		chmod 0640 "$R"%[1]s
	fi
`, shq(c))
	}
	if s.PrestartHook != "" {
		fmt.Fprintf(&b, "\tif [ -z \"$R\" ]; then\n\t\t%s || true\n\tfi\n", s.PrestartHook)
	}
	fmt.Fprintf(&b, `	if live; then
		systemctl daemon-reload || true
		systemctl reset-failed %[1]s 2>/dev/null || true
		systemctl enable --now %[1]s || true
		systemctl try-restart kpclientd.service || true
	fi
fi
`, shq(s.Unit+".service"))
	return b.String()
}

func upstreamPrerm(s service.Service) string {
	var b strings.Builder
	b.WriteString(scriptHead)
	fmt.Fprintf(&b, `unit=%s
old=%s
new=%s
if [ "$1" = remove ] && live; then
	systemctl stop "$unit.service" 2>/dev/null || true
	systemctl disable "$unit.service" 2>/dev/null || true
fi
if [ "$1" = upgrade ] && [ -n "$2" ] && dpkg --compare-versions "$2" lt %s~; then
	if live; then
		systemctl stop "$unit.service" 2>/dev/null || true
		systemctl disable "$unit.service" 2>/dev/null || true
	fi
	if [ -f "$R$new/.kno-migrated-moves" ]; then
		while read -r m; do
			from=${m%%%%=*}
			to=${m#*=}
			if [ -d "$R$to" ] && [ ! -e "$R$from" ]; then
				mkdir -p "$(dirname "$R$from")"
				mv "$R$to" "$R$from"
			fi
		done < "$R$new/.kno-migrated-moves"
		rm -f "$R$new/.kno-migrated-moves"
	fi
	if [ -n "$old" ] && [ -f "$R$new/.kno-migrated" ]; then
		mkdir -p "$R$old"
		while read -r n; do
			if [ -e "$R$new/$n" ] || [ -L "$R$new/$n" ]; then
				mv "$R$new/$n" "$R$old/$n"
			fi
		done < "$R$new/.kno-migrated"
		rm -f "$R$new/.kno-migrated" "$R$new/.kno-migrated-from"
	fi
fi
`, shq(s.Unit), shq(s.LegacyStateDir), shq(s.StateDir), upstreamLayoutSince)
	return b.String()
}

func finishUpstreamConfig(c ConfigRoot, s service.Service, root, pkg string) (string, error) {
	tag, rev, arch, svc := c.Tag, c.Rev, c.Arch, c.Svc
	dst := root + dropInPath(s)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(dst, []byte(dropIn(s)), 0o644); err != nil {
		return "", err
	}
	configs := []string{s.ConfigDst}
	for _, af := range s.AuxFiles {
		if _, d, ok := strings.Cut(af, "="); ok {
			configs = append(configs, d)
		}
	}
	control := fmt.Sprintf(`Package: %s
Version: %s
Architecture: %s
Maintainer: %s
Depends: katzenpost (= %s)
Section: net
Priority: optional
Description: namenlos configuration for %s (%s)
 Rendered configuration and public keys for the namenlos service %s,
 and a drop-in for katzenpost's %s unit. It moves the service's state
 from kno's old layout on upgrade, and back on a downgrade below %s.
`, pkg, configVersion(tag, rev, c.Stamp), arch, Maintainer(), debVersion(tag),
		svc, tag, svc, s.Unit, upstreamLayoutSince)
	if err := os.WriteFile(filepath.Join(root, "DEBIAN/control"), []byte(control), 0o644); err != nil {
		return "", err
	}
	for name, body := range map[string]string{
		"preinst":  upstreamPreinst(pkg, s),
		"postinst": upstreamPostinst(pkg, s, configs),
		"prerm":    upstreamPrerm(s),
	} {
		if err := writeExec(filepath.Join(root, "DEBIAN", name), body); err != nil {
			return "", err
		}
	}
	return root, nil
}
