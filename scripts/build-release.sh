#!/bin/sh
# build-release.sh — cross-compile CGO-free static otelstore binaries.
#
# Produces one static binary per target under dist/ plus dist/SHA256SUMS.
# Pure-Go (CGO_ENABLED=0) means no C toolchain and no per-OS cross-compiler
# is needed — one machine builds all targets.
#
# Usage:  sh scripts/build-release.sh
# Requires: go (1.26+), run from the repo root (where cmd/otelstore lives).

set -eu

OUT=dist
PKG=./cmd/otelstore
# Version stamped into the binary (main.version). Prefer an explicit $VERSION,
# else the current git tag, else "dev".
VERSION="${VERSION:-$(git describe --tags --exact-match 2>/dev/null || git describe --tags 2>/dev/null || echo dev)}"
# Trim paths, strip debug info, and stamp the version.
LDFLAGS="-s -w -X main.version=${VERSION}"

# target list: "GOOS/GOARCH"
TARGETS="darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64"

rm -rf "$OUT"
mkdir -p "$OUT"

for t in $TARGETS; do
    goos=${t%/*}
    goarch=${t#*/}
    name="otelstore-${goos}-${goarch}"
    [ "$goos" = "windows" ] && name="${name}.exe"

    echo "building $name ..."
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
        go build -trimpath -ldflags "$LDFLAGS" -o "$OUT/$name" "$PKG"
done

echo "computing checksums ..."
# sha256sum on Linux; shasum -a 256 on macOS.
if command -v sha256sum >/dev/null 2>&1; then
    ( cd "$OUT" && sha256sum otelstore-* > SHA256SUMS )
else
    ( cd "$OUT" && shasum -a 256 otelstore-* > SHA256SUMS )
fi

echo "done. artifacts in $OUT/:"
ls -1 "$OUT"
