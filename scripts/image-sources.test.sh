#!/usr/bin/env bash
# image-sources.test.sh — the backend image must copy every directory of the
# module that the server binary is built from.
#
# The Dockerfile copies source directories one by one. A new top-level package
# the binary imports builds everywhere except the image, and the failure shows
# up only in the publish job after merge.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no Docker.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root/backend"

readonly DOCKERFILE=deploy/Dockerfile
readonly MAIN_PACKAGE=./cmd/server

module=$(go list -m)
needed=$(go list -deps -f '{{if .Module}}{{if eq .Module.Path "'"$module"'"}}{{.ImportPath}}{{end}}{{end}}' "$MAIN_PACKAGE" |
    sed -n "s|^$module/\([^/]*\).*|\1|p" | sort -u)
copied=$(sed -nE 's|^COPY --link backend/([^/ ]+)/ .*|\1|p' "$DOCKERFILE" | sort -u)

[[ -n "$needed" && -n "$copied" ]] || {
    echo "!!! found no packages or no COPY lines; this check is asserting nothing" >&2
    exit 1
}

missing=$(comm -23 <(printf '%s\n' "$needed") <(printf '%s\n' "$copied"))
# A directory the Dockerfile copies but .dockerignore drops is just as absent.
ignored=$(sed -nE 's|^backend/([^/]+)/$|\1|p' "$root/.dockerignore" | sort -u)
excluded=$(comm -12 <(printf '%s\n' "$needed") <(printf '%s\n' "$ignored"))
if [[ -n "$excluded" ]]; then
    {
        echo "!!! .dockerignore drops directories $MAIN_PACKAGE imports from:"
        printf '      backend/%s/\n' $excluded
    } >&2
    exit 1
fi
if [[ -n "$missing" ]]; then
    {
        echo "!!! $MAIN_PACKAGE imports packages from directories $DOCKERFILE does not copy:"
        printf '      backend/%s/\n' $missing
    } >&2
    exit 1
fi
echo "image sources: $(printf '%s\n' "$needed" | wc -l | tr -d ' ') directories, all copied"
