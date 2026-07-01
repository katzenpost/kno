#!/bin/sh
set -eu

SELF=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
REPO=$(CDPATH='' cd -- "$SELF/.." && pwd)
# shellcheck disable=SC1091
. "$REPO/data/thirdparty/pins.env"

root="${1:?usage: deb-build.sh <root-dir> <output.deb>}"
out="${2:?usage: deb-build.sh <root-dir> <output.deb>}"

PODMAN="${PODMAN:-podman}"
IMAGE="$DEB_IMAGE@$DEB_IMAGE_DIGEST"

command -v "$PODMAN" >/dev/null 2>&1 || { echo "deb-build: $PODMAN not found" >&2; exit 1; }

rootabs=$(CDPATH='' cd -- "$root" && pwd)
outdir=$(CDPATH='' cd -- "$(dirname -- "$out")" && pwd)
outbase=$(basename -- "$out")

"$PODMAN" run --rm \
	-e SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-1600000000}" \
	-v "$rootabs:/pkgroot:ro" \
	-v "$outdir:/out:rw" \
	"$IMAGE" \
	dpkg-deb --root-owner-group --build /pkgroot "/out/$outbase" >/dev/null
