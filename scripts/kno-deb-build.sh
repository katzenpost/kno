#!/bin/sh
set -eu

SELF=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
REPO=$(CDPATH='' cd -- "$SELF/.." && pwd)
# shellcheck disable=SC1091
. "$REPO/data/thirdparty/pins.env"

ver="${1:?usage: kno-deb-build.sh <version> [arch] [outdir]}"
ver="${ver#v}"
case "$ver" in
[0-9]*) ;;
*) echo "kno-deb-build: version must start with a digit, got '$ver'" >&2; exit 1 ;;
esac
arch="${2:-$(dpkg --print-architecture 2>/dev/null || echo amd64)}"
outdir="${3:-$REPO/dist/kno}"

case "$arch" in
	ppc64el) goarch=ppc64le ;;
	armhf)   goarch=arm ;;
	i386)    goarch=386 ;;
	*)       goarch="$arch" ;;
esac
buildarch="${KNO_BUILD_ARCH:-$(uname -m)}"
case "$buildarch" in
	x86_64|amd64) buildarch=amd64 ;;
	aarch64|arm64) buildarch=arm64 ;;
	ppc64le|ppc64el) buildarch=ppc64le ;;
	*) echo "kno-deb-build: unsupported build architecture: $buildarch" >&2; exit 1 ;;
esac
DEB_BUILD="${DEB_BUILD:-$SELF/deb-build.sh}"
PODMAN="${PODMAN:-podman}"
maint="${DEB_MAINTAINER:-Katzenpost <packages@katzenpost.network>}"

command -v "$PODMAN" >/dev/null 2>&1 || { echo "kno-deb-build: $PODMAN not found" >&2; exit 1; }

root="$outdir/.root"
rm -rf "$root"
umask 022
mkdir -p "$root/DEBIAN" "$root/usr/bin"

cache="${KNO_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/katzenpost/kno}"
mkdir -p "$cache/gomodcache" "$cache/gobuild"

log=$(mktemp)
trap 'rm -f "$log"' EXIT INT TERM
set +e
# shellcheck disable=SC2016
"$PODMAN" run --rm --platform "linux/$buildarch" -v "$REPO:/repo:ro" -v "$root/usr/bin:/out:rw" \
	-v "$cache/gomodcache:/gomod:rw" -v "$cache/gobuild:/gocache:rw" \
	-e GOMODCACHE=/gomod -e GOCACHE=/gocache \
	-e CGO_ENABLED=0 -e GOOS=linux -e GOARCH="$goarch" \
	-e KNO_VERSION="$ver" -e GOTOOLCHAIN=local \
	"${BUILD_IMAGE%%:*}@$BUILD_IMAGE_DIGEST" sh -c 'cd /repo && go build -trimpath -buildvcs=false -ldflags "-X github.com/katzenpost/kno/internal/cli.stamped=$KNO_VERSION" -o /out/kno ./cmd/kno' \
	>"$log" 2>&1
rc=$?
set -e
if [ "$rc" -ne 0 ]; then
	cat "$log" >&2
	echo "kno-deb-build: kno build failed" >&2
	exit 1
fi
[ "${KNO_VERBOSE:-0}" = 1 ] && cat "$log" >&2
:

cat > "$root/DEBIAN/control" <<CTRL
Package: kno
Version: $ver
Architecture: $arch
Maintainer: $maint
Depends: ca-certificates
Recommends: podman, git
Section: admin
Priority: optional
Description: katzenpost network organizer (the kno tool)
 The kno binary: build, deploy, update and verify a katzenpost node. Every job
 is a subcommand (kno --help); the repository data it needs is compiled into
 the binary, so a checkout is optional.
CTRL

cat > "$root/DEBIAN/postinst" <<'POST'
#!/bin/sh
set -e
[ "$1" = configure ] || exit 0
[ "${KNO_SKIP_BOOTSTRAP:-0}" != 1 ] || exit 0

if ! /usr/bin/kno bootstrap --package >&2; then
	echo "kno: host bootstrap did not complete; run 'sudo kno bootstrap --package' to retry" >&2
fi

POST
chmod 0755 "$root/DEBIAN/postinst"

mkdir -p "$outdir"
sh "$DEB_BUILD" "$root" "$outdir/kno_${ver}_${arch}.deb"
printf '%s\n' "$outdir/kno_${ver}_${arch}.deb"
