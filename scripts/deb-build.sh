#!/bin/sh
set -eu

SELF=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
REPO=$(CDPATH='' cd -- "$SELF/.." && pwd)
# shellcheck disable=SC1091
. "$REPO/data/thirdparty/pins.env"

root="${1:?usage: deb-build.sh <root-dir> <output.deb>}"
out="${2:?usage: deb-build.sh <root-dir> <output.deb>}"

PODMAN="${PODMAN:-podman}"
buildarch="${KNO_BUILD_ARCH:-$(uname -m)}"
case "$buildarch" in
	x86_64|amd64) buildarch=amd64; digest=$DEB_IMAGE_DIGEST ;;
	aarch64|arm64) buildarch=arm64; digest=$DEB_IMAGE_DIGEST_ARM64 ;;
	ppc64le|ppc64el) buildarch=ppc64le; digest=$DEB_IMAGE_DIGEST_PPC64LE ;;
	*) echo "deb-build: unsupported build architecture: $buildarch" >&2; exit 1 ;;
esac
IMAGE="$DEB_IMAGE@$digest"

command -v "$PODMAN" >/dev/null 2>&1 || { echo "deb-build: $PODMAN not found" >&2; exit 1; }

rootabs=$(CDPATH='' cd -- "$root" && pwd)
outdir=$(CDPATH='' cd -- "$(dirname -- "$out")" && pwd)
outbase=$(basename -- "$out")

"$PODMAN" run --rm --platform "linux/$buildarch" \
	-e SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-1600000000}" \
	-v "$rootabs:/pkgroot:ro" \
	-v "$outdir:/out:rw" \
	"$IMAGE" \
	dpkg-deb --root-owner-group --build /pkgroot "/out/$outbase" >/dev/null
