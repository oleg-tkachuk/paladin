#!/usr/bin/env bash
# e2e-images.sh — carry the images verify-e2e built from one CI job to another.
#
#   scripts/e2e-images.sh save <dir>   after verify-e2e:images, in the build job
#   scripts/e2e-images.sh load <dir>   before verify-e2e:run, in each shard
#
# CI builds both images once and every Playwright shard runs against them,
# instead of each shard building its own. What travels is the images and the
# info.env files their build stamped: verify-e2e.sh picks the image by the
# version info.env names (stack_use_built_image), so a shard with the images
# but not those files would refuse to start, and one with stale files would
# test the wrong build.
#
# Image names and versions come from scripts/stack-ports.sh, the same place
# verify-e2e.sh reads them, so the two cannot disagree.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"
# shellcheck source=SCRIPTDIR/stack-ports.sh
source "$root/scripts/stack-ports.sh"

readonly ARCHIVE=images.tar
readonly INFO_FILES=("$STACK_CORE_INFO" "$STACK_CONSOLE_INFO")

if [ "$#" -ne 2 ]; then
    echo "usage: $0 save|load <dir>" >&2
    exit 2
fi
mode=$1
dir=$2

case "$mode" in
save)
    core=$(stack_built_version "$STACK_CORE_INFO")
    console=$(stack_built_version "$STACK_CONSOLE_INFO")
    mkdir -p "$dir"
    docker save -o "$dir/$ARCHIVE" \
        "${PALADIN_IMAGE_PREFIX}/${STACK_CORE_IMAGE}:${core}" \
        "${PALADIN_IMAGE_PREFIX}/${STACK_CONSOLE_IMAGE}:${console}"
    for f in "${INFO_FILES[@]}"; do
        mkdir -p "$dir/$(dirname "$f")"
        cp "$f" "$dir/$f"
    done
    echo ">>> [e2e-images] saved ${STACK_CORE_IMAGE}:${core} and ${STACK_CONSOLE_IMAGE}:${console}"
    ;;
load)
    docker load -i "$dir/$ARCHIVE" >/dev/null
    for f in "${INFO_FILES[@]}"; do
        mkdir -p "$(dirname "$f")"
        cp "$dir/$f" "$f"
    done
    # The same check verify-e2e.sh makes, here so a broken hand-off fails on
    # the step that caused it.
    stack_use_built_image "$STACK_CORE_TAG_VAR" "$STACK_CORE_INFO" "${PALADIN_IMAGE_PREFIX}/${STACK_CORE_IMAGE}"
    stack_use_built_image "$STACK_CONSOLE_TAG_VAR" "$STACK_CONSOLE_INFO" "${PALADIN_IMAGE_PREFIX}/${STACK_CONSOLE_IMAGE}"
    echo ">>> [e2e-images] loaded ${STACK_CORE_IMAGE}:${PALADIN_CORE_TAG} and ${STACK_CONSOLE_IMAGE}:${PALADIN_CONSOLE_TAG}"
    ;;
*)
    echo "usage: $0 save|load <dir>" >&2
    exit 2
    ;;
esac
