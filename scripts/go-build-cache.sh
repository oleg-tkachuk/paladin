#!/usr/bin/env bash
# go-build-cache.sh — carry the backend image build's Go caches between CI runs.
#
#   scripts/go-build-cache.sh import <dir>   before the image build
#   scripts/go-build-cache.sh export <dir>   after it, on runs that save
#
# The backend Dockerfile compiles with a BuildKit cache mount for Go's build
# cache. A developer's Docker keeps it, so a rebuild takes seconds; a CI runner
# starts empty, so `go build` took about two minutes on every run. Only the
# build cache travels: the module cache is around 2 GB, more to move than the
# download it would save. BuildKit cannot export a cache mount, so this moves its
# contents through two helper targets in that Dockerfile (cache-import,
# cache-export) as one archive in a directory actions/cache persists.
#
# <dir> holds cache.tar. Without it, import does nothing: the build then runs
# cold, exactly as it did before.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
readonly DOCKERFILE="$root/backend/deploy/Dockerfile"
readonly IMPORT_TARGET=cache-import
readonly EXPORT_TARGET=cache-export
# The archive's name; cache-import's ADD names it too.
readonly ARCHIVE=cache.tar

if [ "$#" -ne 2 ]; then
    echo "usage: $0 import|export <dir>" >&2
    exit 2
fi
mode=$1
dir=$2

run=$(date +%s)-$$

case "$mode" in
import)
    if [ ! -s "$dir/$ARCHIVE" ]; then
        echo ">>> [go-build-cache] nothing to import from $dir — the build runs cold"
        exit 0
    fi
    # The archive's directory is the whole build context: cache-import reads
    # nothing else from it.
    docker buildx build --file "$DOCKERFILE" --target "$IMPORT_TARGET" \
        --build-arg "CACHE_RUN=${run}" --output type=cacheonly "$dir"
    echo ">>> [go-build-cache] imported $(du -h "$dir/$ARCHIVE" | cut -f1)"
    ;;
export)
    # cache-export copies from no context at all.
    context=$(mktemp -d "${TMPDIR:-/tmp}/go-build-cache.XXXXXX")
    trap 'rm -rf "$context"' EXIT
    mkdir -p "$dir"
    docker buildx build --file "$DOCKERFILE" --target "$EXPORT_TARGET" \
        --build-arg "CACHE_RUN=${run}" --output "type=tar,dest=${dir}/${ARCHIVE}" "$context"
    echo ">>> [go-build-cache] exported $(du -h "$dir/$ARCHIVE" | cut -f1)"
    ;;
*)
    echo "usage: $0 import|export <dir>" >&2
    exit 2
    ;;
esac
