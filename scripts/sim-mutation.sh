#!/bin/sh
set -eu

KNO="${KNO:-kno}"
NET="${SIM_NET:-knomut}"
SVC="${SIM_BREAK:-mixy-pq-mixserver}"
WORK=$(mktemp -d "${TMPDIR:-/var/tmp}/kno-sim-mutation.XXXXXX")
trap '"$KNO" sim down --net "$NET" --workdir "$WORK" >/dev/null 2>&1 || true; rm -rf "$WORK"' EXIT INT TERM

set -- --net "$NET" --workdir "$WORK"
if [ -n "${SIM_PKGS:-}" ]; then
	"$KNO" sim keys "$@" --pkgs "$SIM_PKGS"
else
	"$KNO" sim keys "$@"
fi
"$KNO" sim configs "$@"

cfg="$WORK/configs/$SVC.toml"
[ -f "$cfg" ] || { echo "sim-mutation: no rendered config $cfg" >&2; exit 1; }
awk 'done == 0 && /^[[:space:]]*Identifier[[:space:]]*=/ { print; print "  NoSuchField = 1"; done = 1; next } { print }' \
	"$cfg" >"$cfg.broken"
mv "$cfg.broken" "$cfg"
grep -q 'NoSuchField' "$cfg" || { echo "sim-mutation: the config was not broken" >&2; exit 1; }

if "$KNO" sim up "$@" >"$WORK/up.log" 2>&1; then
	cat "$WORK/up.log" >&2
	echo "sim-mutation: FAIL: kno sim up accepted a broken $SVC config" >&2
	exit 1
fi
if ! awk -v svc="$SVC" '
	/(refused by their own daemon|exited after launch):$/ { list = 1; next }
	list && /^    / { next }
	list && /^  [^ ]/ { n = $1; sub(/:$/, "", n); if (n == svc) found = 1; next }
	{ list = 0 }
	END { exit !found }' "$WORK/up.log"; then
	cat "$WORK/up.log" >&2
	echo "sim-mutation: FAIL: kno sim up failed, but its list of refused or exited daemons does not name $SVC" >&2
	exit 1
fi
grep -A3 -E 'refused by their own daemon|exited after launch' "$WORK/up.log" >&2
echo "sim-mutation: ok: kno sim up refused the broken $SVC config"
