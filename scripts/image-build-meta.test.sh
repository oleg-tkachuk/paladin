#!/usr/bin/env bash
# image-build-meta.test.sh — the backend image must not bake a placeholder
# version into the binary.
#
# cmd/server/root.go defaults version, commit and buildTime to "dev", "none"
# and "unknown", and the console shows what SystemService.GetVersion returns.
# A Dockerfile ARG with a default, passed to -ldflags unconditionally,
# replaced those with its own placeholder in every image built without
# --build-arg — and the dashboard read "backend undefined · undefined".
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no Docker.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
readonly DOCKERFILE="$root/backend/deploy/Dockerfile"
# ARG name → the main-package variable it sets.
readonly -a METADATA=(APP_VERSION:version GIT_COMMIT_HASH:commit BUILD_TIME:buildTime)

fail=0
for pair in "${METADATA[@]}"; do
    arg=${pair%%:*} var=${pair#*:}
    if grep -E "^ARG ${arg}=" "$DOCKERFILE" >/dev/null; then
        echo "!!! ARG $arg has a default; an image built without it bakes that into main.$var" >&2
        fail=1
    fi
    # The -X flag must be conditional on the ARG being set.
    if ! grep -F "\${${arg}:+-X 'main.${var}=\${${arg}}'}" "$DOCKERFILE" >/dev/null; then
        echo "!!! -X main.$var is not conditional on $arg being set" >&2
        fail=1
    fi
done

[[ "$fail" == 0 ]] || exit 1
echo "backend image: build metadata is set only when given"
