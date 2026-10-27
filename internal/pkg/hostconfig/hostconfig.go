package hostconfig

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/pkg/deb"
)

const controlTemplate = `Package: kno-host-config
Version: %s
Architecture: all
Maintainer: %s
Depends: debconf, apt-utils
Section: admin
Priority: optional
Description: Operator identity + policy for a katzenpost node (kno.toml)
 Prompts for which %s nodes this host runs and the update/push policy, then
 writes /etc/katzenpost/kno.toml. Personalized answers are remembered; an
 edited file is never clobbered.
`

const templatesTemplate = `Template: kno-host-config/this_host
Type: boolean
Default: true
Description: Is kno being configured to run on this host, ${hostname}?
 Answer yes if ${hostname} runs katzenpost node(s) that kno should build and
 update here -- you will be asked which nodes and how to update them. Answer no
 only if this host is used to push updates to other hosts.

Template: kno-host-config/network
Type: string
Default: %s
Description: katzenpost network name for this host.

Template: kno-host-config/this_node
Type: select
Choices: %s
Description: Which node does this host run?
 This host runs one node; pick it. The default matches this host's name when it
 can. Its services are configured to run and update here; you are not asked about
 pushing to other hosts.

Template: kno-host-config/nodes
Type: multiselect
Choices: %s
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
`

const configScript = `#!/bin/sh
set -e
. /usr/share/debconf/confmodule
db_capb backup

hn=$(hostname -s 2>/dev/null || hostname 2>/dev/null || echo "this host")
db_subst kno-host-config/this_host hostname "$hn"

db_fget kno-host-config/this_node seen || RET=false
if [ "$RET" != true ]; then
	for n in @NODELIST@; do
		if [ "$n" = "$hn" ]; then
			db_set kno-host-config/this_node "$n" || true
			break
		fi
	done
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
`

const postinstScript = `#!/bin/sh
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
	db_fget kno-host-config/this_node seen || RET=false
	hn=$(hostname -s 2>/dev/null || hostname)
	if [ "$RET" != true ] && [ "$nodes" != "$hn" ]; then
		nodes=""
		echo "kno-host-config: this host has the name of no node, so it" >&2
		echo "kno-host-config: claims none. To pick one, run:" >&2
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

`

func AssembleRoot(repo, network, version, rootParent string) (string, error) {
	if network == "" {
		network = "namenlos"
	}
	if version == "" {
		version = "0.0.0"
	}
	nodes := config.ListNodes(repo, network)
	if len(nodes) == 0 {
		return "", fmt.Errorf("no nodes for network %s", network)
	}
	choices := strings.Join(nodes, ", ")

	nodelist := strings.Join(nodes, " ") + " "

	root := filepath.Join(rootParent, "kno-host-config")
	if err := os.RemoveAll(root); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(root, "DEBIAN"), 0o755); err != nil {
		return "", err
	}

	files := []struct {
		name string
		body string
		mode os.FileMode
	}{
		{"control", fmt.Sprintf(controlTemplate, version, deb.Maintainer(), network), 0o644},
		{"templates", fmt.Sprintf(templatesTemplate, network, choices, choices), 0o644},
		{"config", strings.Replace(configScript, "@NODELIST@", nodelist, 1), 0o755},
		{"postinst", postinstScript, 0o755},
	}
	for _, f := range files {
		p := filepath.Join(root, "DEBIAN", f.name)
		if err := os.WriteFile(p, []byte(f.body), f.mode); err != nil {
			return "", err
		}

		if err := os.Chmod(p, f.mode); err != nil {
			return "", err
		}
	}
	return root, nil
}

func Package(ctx context.Context, repo, network, version, outdir string) (string, error) {
	if version == "" {
		version = "0.0.0"
	}
	parent := filepath.Join(outdir, ".pkgbuild")
	root, err := AssembleRoot(repo, network, version, parent)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(outdir, 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(outdir, "kno-host-config_"+version+"_all.deb")
	if err := deb.Build(ctx, root, out); err != nil {
		return "", err
	}
	os.RemoveAll(parent)
	return out, nil
}
