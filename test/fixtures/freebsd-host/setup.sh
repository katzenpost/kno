#!/bin/sh
set -eu

U="$1"
D=/var/tmp/fixture
J=/usr/jails
R=$J/respectmy
A=$J/allmixedup
RS=/var/lib/pq-katzenpost-authority
AS=/var/lib/pq-katzenpost-mixserver
RC=/usr/local/etc/katzenpost/authority/config.toml
AC=/usr/local/etc/katzenpost/allmixedup-pq-gatewaynode.toml

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

mkjail() {
	quiet "create-$1" ezjail-admin create "$1" "$2"
	cp /etc/resolv.conf "$J/$1/etc/resolv.conf"
	echo "$1 $2"
}

mkdir -p "$D/kp" "$D/keys"
for f in base.txz katzenpost.pkg keys.tar respectmy-pq-authority.toml allmixedup-pq-gatewaynode.toml \
	katzenpost.rc allmixedup.rc katzenpost-wrapper.respectmy katzenpost-wrapper.allmixedup \
	update-mixes update-respectmy update-allmixedup autobuild pf.conf; do
	fetch -q -o "$D/$f" "$U/$f"
done

step "pkg and ezjail in the host"
env ASSUME_ALWAYS_YES=yes pkg bootstrap -f >/dev/null
pkg install -y ezjail >/dev/null
pkg query '%n-%v' ezjail

step "lo42 for the jail addresses, as on the jail host, pf nat for mixbuilder only"
sysrc cloned_interfaces="bridge23 lo42" ifconfig_lo42_ipv6="inet6 -ifdisabled" pf_enable=YES ezjail_enable=YES >/dev/null
ifconfig bridge23 >/dev/null 2>&1 || ifconfig bridge23 create
ifconfig lo42 >/dev/null 2>&1 || ifconfig lo42 create
ifconfig lo42 inet6 -ifdisabled
install -m 600 "$D/pf.conf" /etc/pf.conf
service pf start >/dev/null
pfctl -sn
pfctl -sr

step "ezjail basejail from base.txz"
quiet install ezjail-admin install -h "file://$D"
mkjail mixbuilder 'lo42|10.42.0.12'
mkjail respectmy 'lo42|10.42.0.13,lo42|2a01:4f9:3100:4b60:1013::1'
mkjail allmixedup 'lo42|10.42.0.10,lo42|2a01:4f9:3100:4b60:1010::1'
ezjail-admin start >/dev/null 2>&1
jls

step "go and git in mixbuilder"
quiet mixbuilder-pkg ezjail-admin console -e 'env ASSUME_ALWAYS_YES=yes pkg install -y go git' mixbuilder
jexec mixbuilder sh -c 'go version; git --version'

step "the v0.0.107 daemons at the old hand-install paths"
tar -xf "$D/katzenpost.pkg" -C "$D/kp" /usr/local/sbin/kpdirauth /usr/local/sbin/kpserver 2>/dev/null
tar -xf "$D/keys.tar" -C "$D/keys"
install -d "$R/usr/local/sbin" "$R/usr/local/etc/katzenpost/authority" "$R/usr/local/etc/rc.d"
install -d "$A/usr/local/bin" "$A/usr/local/etc/katzenpost" "$A/usr/local/etc/rc.d"
install -d -m 700 "$R$RS" "$A$AS"
install -m 755 "$D/kp/usr/local/sbin/kpdirauth" "$R/usr/local/sbin/katzenpost-dirauth"
install -m 755 "$D/kp/usr/local/sbin/kpserver" "$A/usr/local/bin/server"
install -m 644 "$D/respectmy-pq-authority.toml" "$R$RC"
install -m 644 "$D/allmixedup-pq-gatewaynode.toml" "$A$AC"
install -m 755 "$D/katzenpost.rc" "$R/usr/local/etc/rc.d/katzenpost"
install -m 755 "$D/allmixedup.rc" "$A/usr/local/etc/rc.d/katzenpost"
install -m 755 "$D/katzenpost-wrapper.respectmy" "$R/usr/local/sbin/katzenpost-wrapper"
install -d "$A/usr/local/sbin"
install -m 755 "$D/katzenpost-wrapper.allmixedup" "$A/usr/local/sbin/katzenpost-wrapper"
for d in "$D"/keys/*/; do
	install -m 644 "$d"*.pem "$R$RS/"
done
cp -R "$D"/keys/* "$R$RS/"
sysrc -f "$R/etc/rc.conf" katzenpost_enable=YES katzenpost_config="$RC"
sysrc -f "$A/etc/rc.conf" katzenpost_enable=YES katzenpost_config="$AC"

step "keys from the old binaries"
quiet gen-respectmy jexec respectmy /usr/local/sbin/katzenpost-dirauth -f "$RC" -g
quiet gen-allmixedup jexec allmixedup /usr/local/bin/server -f "$AC" -g
awk -v id="$R$RS/identity.public.pem" -v ln="$R$RS/link.public.pem" '
function slurp(f,  l, s) { s = ""; while ((getline l < f) > 0) s = s l "\n"; close(f); return s }
BEGIN { k["IdentityPublicKey"] = slurp(id); k["LinkPublicKey"] = slurp(ln) }
skip { if (index($0, "\"\"\"")) skip = 0; next }
/^[ \t]*\[/ { me = 0 }
/^[ \t]*Identifier = "respectmy"/ { me = 1 }
me && ($1 in k) && $2 == "=" && index($3, "\"\"\"") == 1 {
	printf "%s = \"\"\"%s\"\"\"\n", $1, k[$1]
	skip = 1
	next
}
{ print }
' "$R$RC" >"$D/config.toml"
diff "$R$RC" "$D/config.toml" | grep -c '^[<>]' || true
install -m 644 "$D/config.toml" "$R$RC"
ls -l "$R$RS"/identity.* "$R$RS"/link.* "$A$AS"

step "the old updater: its scripts, the mixbuilder autobuild and the cron entry"
install -d -m 755 /root/bin
for f in update-mixes update-respectmy update-allmixedup; do
	install -m 755 "$D/$f" "/root/bin/$f"
done
install -m 755 "$D/autobuild" "$J/mixbuilder/root/autobuild"
grep -q update-mixes /etc/crontab || printf '\n23,53\t*\t*\t*\t*\troot\t/root/bin/update-mixes\n' >>/etc/crontab
grep update-mixes /etc/crontab

step "the old daemons run, as on the jail host"
for j in respectmy allmixedup; do
	quiet "start-$j" ezjail-admin console -e "service katzenpost start" "$j"
done
sleep 5
: >"$R$RS/persistence.db"
for j in respectmy allmixedup; do pgrep -lf -j "$j" 'katzenpost-wrapper|katzenpost-dirauth|server -f'; done
sockstat -46 -l -j respectmy
sockstat -46 -l -j allmixedup

step "go and git on the host, as the operator installed them"
quiet host-pkg env ASSUME_ALWAYS_YES=yes pkg install -y go git
go version

rm -rf "$D"
echo SETUP_RC=0
