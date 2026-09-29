#!/bin/sh
# shellcheck disable=SC2016
set -u

root="${JAIL_ROOT:-/usr/jails}"
jails="${JAILS:-respectmy allmixedup}"

section() { printf '\n## %s\n' "$*"; }
run() {
	printf '$ %s\n' "$*"
	sh -c "$*" 2>&1 | sed 's/^/  /'
}

printf '# kno FreeBSD host inventory\n'
printf 'Read-only. It prints file names, modes, sizes and sha256 sums. It never prints a key.\n'
printf 'Run it as root. Send the whole output back.\n'

section host
run 'hostname'
run 'freebsd-version -ku'
run 'uname -m'
run 'id -un'
run 'df -h / /usr/jails 2>/dev/null'
run 'for t in go git pkg ezjail-admin jls jexec service sudo; do printf "%s: " $t; command -v $t || echo absent; done'
run 'go version 2>/dev/null'
run 'pkg info -q 2>/dev/null | grep -E "^(go|git|ezjail|katzenpost|kno|namenlos)" '

section jails
run 'jls -h jid name host.hostname path ip4.addr ip6.addr 2>/dev/null || jls'
run 'ls -la /usr/local/etc/ezjail/ 2>/dev/null'
run 'grep -E "^export jail_" /usr/local/etc/ezjail/* 2>/dev/null | grep -E "rootdir|ip|parameters|hostname"'
run 'grep -E "^(ezjail_|jail_|cloned_interfaces|ifconfig_lo1|pf_)" /etc/rc.conf 2>/dev/null'
run 'ifconfig lo1 2>/dev/null'

section cron
run 'grep -v "^#" /etc/crontab | grep -v "^$"'
run 'ls -la /etc/cron.d /usr/local/etc/cron.d 2>/dev/null'
run 'crontab -l -u root 2>/dev/null'
run 'ls -la /root/bin 2>/dev/null'

for j in $jails; do
	jr="$root/$j"
	section "jail $j ($jr)"
	run "grep -i katzenpost $jr/etc/rc.conf $jr/etc/rc.conf.d/* $jr/usr/local/etc/rc.conf.d/* 2>/dev/null"
	run "ls -la $jr/usr/local/etc/rc.d/ 2>/dev/null"
	for f in "$jr"/usr/local/etc/rc.d/*; do
		[ -f "$f" ] || continue
		printf '$ cat %s\n' "$f"
		sed 's/^/  /' "$f"
	done
	run "ls -la $jr/usr/local/bin $jr/usr/local/sbin 2>/dev/null | grep -i -E 'katzen|server|dirauth|kp|courier|replica|fetch|ping'"
	run "find $jr/usr/local/etc/katzenpost -type f -exec sha256 -r {} + 2>/dev/null"
	run "grep -h -E '^[[:space:]]*(DataDir|Identifier|Addresses|BindAddresses|File|Level)[[:space:]]*=' $jr/usr/local/etc/katzenpost/*.toml $jr/usr/local/etc/katzenpost/*/*.toml 2>/dev/null"
	for d in "$jr"/var/lib/*katzenpost* "$jr"/var/lib/katzenpost; do
		[ -d "$d" ] || continue
		run "ls -lan $d"
		run "find $d -maxdepth 1 -type f -name '*public*' -exec sha256 -r {} +"
	done
	run "pkg -c $jr info -q 2>/dev/null"
	run "ps -axww -o pid,ppid,jid,user,command -J \$(jls -j $j jid 2>/dev/null || echo 0) 2>/dev/null"
	run "jexec $j service katzenpost status 2>/dev/null"
	run "sockstat -46l -j $j 2>/dev/null"
done

section end
printf 'inventory complete\n'
