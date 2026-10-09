#!/usr/bin/env bash
# compose-images.test.sh — the end-to-end stack runs the images the backend's
# stack runs.
#
# Renovate's config:recommended ignores every **/tests/** path, so it moved the
# backend stack's images and left the end-to-end stack, under frontend/tests,
# behind. renovate.json now scans it; this fails when Renovate is told to
# ignore it again, or when a third-party image differs between the two stacks.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
readonly BACKEND_COMPOSE=backend/deploy/docker-compose.yaml
readonly E2E_COMPOSE=frontend/tests/e2e/docker-compose.test.yaml
readonly RENOVATE_CONFIG=renovate.json

# image_name <ref>: the image a ref names, without its tag or digest.
image_name() {
    local ref=${1%%@*}
    local last=${ref##*/}
    [[ $last == *:* ]] && ref=${ref%:*}
    echo "$ref"
}

# ignored_by <path> <glob>: whether a Renovate ignorePaths glob covers a path.
# Inside [[ ]] a * matches "/" too, so ** needs no special case.
ignored_by() {
    # shellcheck disable=SC2053 # the glob is the point
    [[ $1 == $2 ]]
}

for case in "postgres:18.6=postgres" "postgres:18.6@sha256:abc=postgres" "chrislusf/seaweedfs:4.48=chrislusf/seaweedfs" "registry.local:5000/team/app:1.2=registry.local:5000/team/app" "paladin-core=paladin-core"; do
    got=$(image_name "${case%=*}")
    [ "$got" = "${case#*=}" ] || { echo "FAIL image_name ${case%=*} = $got, want ${case#*=}" >&2; exit 1; }
done
ignored_by "$E2E_COMPOSE" "**/tests/**" || { echo "FAIL **/tests/** does not cover $E2E_COMPOSE" >&2; exit 1; }
if ignored_by "$E2E_COMPOSE" "**/node_modules/**"; then
    echo "FAIL **/node_modules/** covers $E2E_COMPOSE" >&2
    exit 1
fi

cd "$root"
failed=0

ignore_paths=$(jq -r '.ignorePaths // empty | .[]' "$RENOVATE_CONFIG")
[ -n "$ignore_paths" ] || { echo "FAIL $RENOVATE_CONFIG sets no ignorePaths, so config:recommended's ignores $E2E_COMPOSE" >&2; exit 1; }
while IFS= read -r glob; do
    if ignored_by "$E2E_COMPOSE" "$glob"; then
        echo "FAIL $RENOVATE_CONFIG ignorePaths $glob hides $E2E_COMPOSE from Renovate" >&2
        failed=1
    fi
done <<<"$ignore_paths"

# images <file>: the third-party images a stack runs. A ref built from
# variables is one of this repository's own images, which neither pins.
images() {
    yq --yaml-fix-merge-anchor-to-spec=true '.services[].image' "$1" | grep -v '\$' | sort -u
}

backend_refs=$(images "$BACKEND_COMPOSE")

# backend_ref <name>: the ref the backend stack runs an image at, if any.
backend_ref() {
    local ref
    while IFS= read -r ref; do
        [ "$(image_name "$ref")" = "$1" ] && { echo "$ref"; return; }
    done <<<"$backend_refs"
}

compared=0
while IFS= read -r ref; do
    want=$(backend_ref "$(image_name "$ref")")
    [ -n "$want" ] || continue
    compared=$((compared + 1))
    if [ "$ref" != "$want" ]; then
        echo "FAIL $E2E_COMPOSE runs $ref, $BACKEND_COMPOSE runs $want" >&2
        failed=1
    fi
done < <(images "$E2E_COMPOSE")

[ "$failed" -eq 0 ] || exit 1
[ "$compared" -gt 0 ] || { echo "FAIL the two stacks share no third-party image; $0 compares nothing" >&2; exit 1; }
echo "ok   the end-to-end stack runs the backend stack's images ($compared shared)"
