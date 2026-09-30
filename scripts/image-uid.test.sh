#!/usr/bin/env bash
# image-uid.test.sh — the UID and GID an image runs as must be the ones its
# chart runs it as.
#
# The Dockerfile declares them (ARG APP_UID / APP_GID, used by USER and every
# --chown) and the chart repeats them in podSecurityContext and
# securityContext. A chart UID the image did not build for starts the pod as a
# user that owns none of the files it has to write.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no Docker.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

readonly COMPONENTS=(backend frontend)

command -v yq >/dev/null 2>&1 || {
    echo "!!! yq is not installed; the image UID contract went unchecked" >&2
    exit 1
}

fail=0
bad() { echo "!!! $*" >&2; fail=1; }

arg_of() { sed -n "s/^ARG $2=\([0-9][0-9]*\)$/\1/p" "$1" | head -1; }

for component in "${COMPONENTS[@]}"; do
    dockerfile="$component/deploy/Dockerfile"
    values="$component/deploy/chart/values.yaml"
    uid=$(arg_of "$dockerfile" APP_UID)
    gid=$(arg_of "$dockerfile" APP_GID)
    [[ -n "$uid" && -n "$gid" ]] || { bad "$dockerfile declares no numeric APP_UID / APP_GID"; continue; }
    grep -q '^USER \${APP_UID}:\${APP_GID}$' "$dockerfile" || bad "$dockerfile: USER is not \${APP_UID}:\${APP_GID}"

    for path in .podSecurityContext.runAsUser .securityContext.runAsUser; do
        got=$(yq -r "$path // \"\"" "$values")
        [[ "$got" == "$uid" ]] || bad "$values: $path is '$got', the image runs as $uid"
    done
    for path in .podSecurityContext.runAsGroup; do
        got=$(yq -r "$path // \"\"" "$values")
        [[ "$got" == "$gid" ]] || bad "$values: $path is '$got', the image runs as group $gid"
    done
done

[[ "$fail" == 0 ]] || exit 1
echo "images: ${#COMPONENTS[@]} run as the UID and GID their charts set"
