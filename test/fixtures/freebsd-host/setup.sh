#!/bin/sh
set -eu

U="$1"
D=/var/tmp/fixture
K=/var/tmp/kno-node-keys

step() {
	echo "== $*"
}

quiet() {
	l="$D/$1.log"
	shift
	"$@" >"$l" 2>&1 || {
		tail -20 "$l"
		exit 1
	}
}

mkdir -p "$D/kp"
for f in base.txz katzenpost.pkg pf.conf \
	respectmy-pq-authority.toml allmixedup-pq-gatewaynode.toml; do
	fetch -q -o "$D/$f" "$U/$f"
done

step "pkg and ezjail in the host"
env ASSUME_ALWAYS_YES=yes pkg bootstrap -f >/dev/null
pkg install -y ezjail >/dev/null
pkg query '%n-%v' ezjail

step "lo42 for the jail addresses, and pf nat for the jails"
sysrc cloned_interfaces="lo42" ifconfig_lo42_ipv6="inet6 -ifdisabled" \
	pf_enable=YES ezjail_enable=YES >/dev/null
ifconfig lo42 >/dev/null 2>&1 || ifconfig lo42 create
ifconfig lo42 inet6 -ifdisabled
install -m 600 "$D/pf.conf" /etc/pf.conf
service pf start >/dev/null
pfctl -sn
pfctl -sr

step "ezjail basejail from base.txz"
quiet install ezjail-admin install -h "file://$D"

nodekeys() {
	install -d -m 700 "$4" "$K/$1"
	quiet "keys-$1" "$D/kp/usr/local/sbin/$2" -f "$D/$3.toml" -g
	mv "$4"/*.pem "$K/$1/"
	rm -rf "$4"
	ls "$K/$1"
}

step "node keys from the v0.0.107 daemons"
tar -xf "$D/katzenpost.pkg" -C "$D/kp" \
	/usr/local/sbin/kpdirauth /usr/local/sbin/kpserver 2>/dev/null
nodekeys kpauthority kpdirauth respectmy-pq-authority \
	/var/lib/pq-katzenpost-authority
nodekeys kpserver kpserver allmixedup-pq-gatewaynode \
	/var/lib/pq-katzenpost-mixserver

rm -rf "$D"
echo SETUP_RC=0
