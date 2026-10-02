#!/usr/bin/env bash
# go-build-cache.test.sh — scripts/go-build-cache.sh round-trips the backend
# image's go build cache through BuildKit.
#
# On a fresh, throwaway builder, so the developer's own cache is not read or
# touched: an archive holding a marker is imported into the cache mount and
# exported back, and the marker must survive. A directory with no archive must
# import nothing and still succeed — CI's cold start.
#
# Runs as `task -t Taskfile.dev.yaml verify:go-build-cache`. Requires Docker
# with buildx and network to pull the Dockerfile's Go base image.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
script="$root/scripts/go-build-cache.sh"
readonly BUILDER=paladin-go-build-cache-test
readonly MARKER_DIR=go-build/zz
readonly MARKER=round-trip-marker

work=$(mktemp -d "${TMPDIR:-/tmp}/go-build-cache-test.XXXXXX")
cleanup() {
    docker buildx rm -f "$BUILDER" >/dev/null 2>&1 || true
    rm -rf "$work"
}
trap cleanup EXIT

docker buildx create --name "$BUILDER" --driver docker-container >/dev/null
export BUILDX_BUILDER=$BUILDER

failed=0
fail() { printf 'FAIL %s\n' "$1"; failed=1; }

mkdir -p "$work/empty"
"$script" import "$work/empty" >/dev/null || fail "import with no archive did not succeed"

mkdir -p "$work/src/$MARKER_DIR" "$work/in"
printf '%s\n' "$MARKER" >"$work/src/$MARKER_DIR/$MARKER"
tar -C "$work/src" -cf "$work/in/cache.tar" go-build
"$script" import "$work/in" >/dev/null 2>&1 || fail "import of an archive failed"
"$script" export "$work/out" >/dev/null 2>&1 || fail "export failed"
tar -tf "$work/out/cache.tar" 2>/dev/null | grep -q "$MARKER_DIR/$MARKER" ||
    fail "the exported cache lost the imported marker"

if [ "$failed" -ne 0 ]; then
    exit 1
fi
printf '%s\n' "go build cache: an imported archive survives export; no archive imports nothing"
