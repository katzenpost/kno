#!/bin/sh
set -eu

SELF=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
REPO=$(CDPATH='' cd -- "$SELF/.." && pwd)
# shellcheck disable=SC1091
. "$REPO/data/thirdparty/pins.env"

net="${1:-namenlos}"
ver="${2:-0.0.0}"
outdir="${3:-$REPO/dist/host-config}"
DEB_BUILD="${DEB_BUILD:-$SELF/deb-build.sh}"

KNO_BIN="${KNO_BIN:-}"
if [ -z "$KNO_BIN" ]; then
	KNO_BIN="$outdir/.kno"
	mkdir -p "$outdir"
	( cd "$REPO" && go build -o "$KNO_BIN" ./cmd/kno )
fi
nodes=$(KNO_REPO="$REPO" "$KNO_BIN" config --list-nodes --network "$net")
choices=$(printf '%s\n' "$nodes" | paste -sd, - | sed 's/,/, /g')
[ -n "$choices" ] || { echo "host-config-build: no nodes for network $net" >&2; exit 1; }
nodelist=$(printf '%s\n' "$nodes" | tr '\n' ' ')

root="$outdir/.pkgroot"
rm -rf "$root"
umask 022
mkdir -p "$root/DEBIAN"

cat > "$root/DEBIAN/control" <<CTRL
Package: kno-host-config
Version: $ver
Architecture: all
Maintainer: ${DEB_MAINTAINER:-Katzenpost <packages@katzenpost.network>}
Depends: debconf, apt-utils
Section: admin
Priority: optional
Description: Operator identity + policy for a katzenpost node (kno.toml)
 Prompts for which $net nodes this host runs and the update/push policy, then
 writes /etc/katzenpost/kno.toml. Personalized answers are remembered; an
 edited file is never clobbered.
CTRL

cat > "$root/DEBIAN/templates" <<TPL
Template: kno-host-config/this_host
Type: boolean
Default: true
Description: Is kno being configured to run on this host, \${hostname}?
 Answer yes if \${hostname} runs katzenpost node(s) that kno should build and
 update here -- you will be asked which nodes and how to update them. Answer no
 only if this host is used to push updates to other hosts.

Template: kno-host-config/network
Type: string
Default: $net
Description: katzenpost network name for this host.

Template: kno-host-config/this_node
Type: select
Choices: none, $choices
Default: none
Description: Which node does this host run?
 This host runs one node; pick it. The default is the node with the name of
 this host, or none. Its services are configured to run and update here; you
 are not asked about pushing to other hosts.

Template: kno-host-config/nodes
Type: multiselect
Choices: $choices
Description: Which nodes do you manage from this host?
 Only for a host that pushes updates to other hosts. Select the nodes you deploy
 from here.

Template: kno-host-config/policy
Type: select
Choices: off, simulate, apply
Default: simulate
Description: Update policy for this host.
 off = never touch the node; simulate = dry-run only; apply = install updates.

Template: kno-host-config/require_signed_tag
Type: boolean
Default: false
Description: Only accept releases whose git tag is OpenPGP-signed?

Template: kno-host-config/push_mode
Type: select
Choices: copy, install, full
Default: full
Description: Operator-side push mode.
 copy stages the packages on the host without root. install also installs them. full also restarts the services.

Template: kno-host-config/privilege
Type: select
Choices: none, sudo, kno, apt
Default: none
Description: Who runs the steps that need root on this host?
 none = nobody automatically; kno prints the commands and you run them.
 sudo = you do, with the sudo rights you already have; no new policy is written.
 kno = kno does, unattended, with the sudo rights that you give its account.
 apt = this host's apt policy installs from kno's local repository.
 .
 none is the default and grants nothing. A host that grants nothing still
 builds every package and reports when its digests disagree with the release,
 which is the work worth having on every node.

Template: kno-host-config/scheduler
Type: select
Choices: none, timer, cron
Default: none
Description: How should this host check for updates?
 none = only when you run 'kno update' by hand; timer = a systemd timer;
 cron = /etc/cron.d.
TPL

cat > "$root/DEBIAN/config" <<'CFG'
#!/bin/sh
set -e
. /usr/share/debconf/confmodule
db_capb backup

hn=$(hostname -s 2>/dev/null || hostname 2>/dev/null || echo "this host")
db_subst kno-host-config/this_host hostname "$hn"

db_fget kno-host-config/this_node seen || RET=false
if [ "$RET" != true ]; then
	v=none
	for n in @NODELIST@; do
		if [ "$n" = "$hn" ]; then
			v=$n
			break
		fi
	done
	db_set kno-host-config/this_node "$v" || true
fi

state=1
while [ "$state" -ge 1 ]; do
	q=""
	case "$state" in
		1) q=this_host ;;
		2) q=network ;;
		3) db_get kno-host-config/this_host
		   if [ "$RET" = true ]; then q=this_node; else q=nodes; fi ;;
		4) db_get kno-host-config/this_host
		   if [ "$RET" = true ]; then q=policy; else q=push_mode; fi ;;
		5) db_get kno-host-config/this_host
		   if [ "$RET" = true ]; then q=require_signed_tag; fi ;;
		6) db_get kno-host-config/this_host
		   if [ "$RET" = true ]; then q=scheduler; fi ;;
		7) db_get kno-host-config/this_host
		   if [ "$RET" = true ]; then q=privilege; fi ;;
		*) break ;;
	esac
	if [ -z "$q" ]; then
		state=$((state + 1))
		continue
	fi
	db_input high "kno-host-config/$q" || true
	if db_go; then
		state=$((state + 1))
	else
		state=$((state - 1))
	fi
done
CFG
sed "s/@NODELIST@/$nodelist/" "$root/DEBIAN/config" >"$root/DEBIAN/config.new"
cat "$root/DEBIAN/config.new" >"$root/DEBIAN/config"
rm -f "$root/DEBIAN/config.new"

cat > "$root/DEBIAN/postinst" <<'POST'
#!/bin/sh
set -e
. /usr/share/debconf/confmodule
CONF=/etc/katzenpost/kno.toml
MARKER="# managed by kno-host-config"

if [ -f "$CONF" ] && ! head -1 "$CONF" | grep -q "$MARKER"; then
	echo "kno-host-config: $CONF was edited by hand; leaving it untouched" >&2
	exit 0
fi

db_get kno-host-config/network; net="$RET"
db_get kno-host-config/this_host; this_host="$RET"
if [ "$this_host" = true ]; then
	db_get kno-host-config/this_node; nodes="$RET"
	if [ "$nodes" = none ]; then
		nodes=""
		echo "kno-host-config: this host claims no node. To pick one, run:" >&2
		echo "  sudo dpkg-reconfigure kno-host-config" >&2
	fi
else
	db_get kno-host-config/nodes; nodes="$RET"
fi
db_get kno-host-config/policy; policy="$RET"
db_get kno-host-config/require_signed_tag; signed="$RET"
db_get kno-host-config/push_mode; mode="$RET"
db_get kno-host-config/scheduler; sched="$RET"
db_get kno-host-config/privilege; priv="$RET"
h=$(hostname -s 2>/dev/null || hostname 2>/dev/null || echo host)

nn=""
plain=""
oldifs=$IFS; IFS=','
for n in $nodes; do
	n=$(printf '%s' "$n" | tr -d ' ')
	[ -n "$n" ] && nn="$nn\"$n\", " && plain="$plain$n "
done
IFS=$oldifs
nn="${nn%, }"
plain="${plain% }"

mkdir -p /etc/katzenpost
cat > "$CONF" <<EOF
$MARKER (edit freely; remove this line to keep your edits on upgrade)
network = "$net"
nodes = [$nn]

[update]
policy = "$policy"
require_signed_tag = $signed

[push]
mode = "$mode"
EOF
if [ "$this_host" = true ]; then
	cat >> "$CONF" <<EOF

[hosts."$h"]
hostname = "$h"
scheduler = "$sched"
privilege = "$priv"
EOF
fi
if [ -z "$2" ]; then
	echo "kno-host-config: wrote $CONF" >&2
	if [ "$this_host" = true ]; then
		echo "kno-host-config: this host ($h) is configured to run: $plain" >&2
	else
		echo "kno-host-config: this host ($h) is configured to remotely organize: $plain" >&2
	fi
fi

if [ "$sched" != none ] && command -v kno >/dev/null 2>&1; then
	SUDO= kno schedule "$sched" >&2 ||
		echo "kno-host-config: could not install the $sched scheduler; run: sudo kno schedule $sched" >&2
fi

POST

chmod 0755 "$root/DEBIAN/config" "$root/DEBIAN/postinst"

mkdir -p "$outdir"
deb="$outdir/kno-host-config_${ver}_all.deb"
sh "$DEB_BUILD" "$root" "$deb"
printf '%s\n' "$deb"
