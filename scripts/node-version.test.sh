#!/usr/bin/env bash
# node-version.test.sh — one Node major for the console, wherever it is named.
#
# .nvmrc is the Node a developer runs (`nvm use`). The console image builds on
# frontend/deploy/Dockerfile's NODE_VERSION, and CI runs the console's suite on
# ci.yaml's NODE_VERSION. A developer on another major than the image passes
# locally what the image then fails, so the three must agree.
#
# .github/ci/.node-version is not among them: it pins the Node the CI scripts
# run on, which moves on its own.
#
# Runs from `task -t Taskfile.dev.yaml verify-repo`.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

readonly NVMRC=.nvmrc
readonly DOCKERFILE=frontend/deploy/Dockerfile
readonly WORKFLOW=.github/workflows/ci.yaml
# The key each file names the version under.
readonly DOCKER_ARG=NODE_VERSION
readonly WORKFLOW_ENV=NODE_VERSION

command -v yq >/dev/null 2>&1 || {
    echo "!!! yq is not installed; the Node versions went unchecked — https://github.com/mikefarah/yq#install" >&2
    exit 1
}

failed=0
want=""
if [ -f "$NVMRC" ]; then
    want=$(tr -d '[:space:]' <"$NVMRC")
fi
if [ -z "$want" ]; then
    echo "FAIL $NVMRC is missing or empty: it names the Node the console is developed on" >&2
    exit 1
fi

docker=$(sed -nE "s/^ARG $DOCKER_ARG=([^[:space:]]+).*/\\1/p" "$DOCKERFILE")
workflow=$(yq -r ".env.$WORKFLOW_ENV // \"\"" "$WORKFLOW")
for pair in "$DOCKERFILE $DOCKER_ARG|$docker" "$WORKFLOW env.$WORKFLOW_ENV|$workflow"; do
    where=${pair%%|*}
    got=${pair#*|}
    if [ "$got" != "$want" ]; then
        echo "FAIL $where is '${got}', $NVMRC is '$want'" >&2
        failed=1
    fi
done

if [ "$failed" -ne 0 ]; then
    echo "!!! move the Node major in $NVMRC, $DOCKERFILE and $WORKFLOW together" >&2
    exit 1
fi
echo "node: $NVMRC, the console image and CI all run Node $want"
