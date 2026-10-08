#!/usr/bin/env bash
# badge-versions.sh — the versions of the stack the README's badges show, as
# JSON, read from the files that pin them.
#
#   go, connect, cedar, mcp   backend/go.mod, through `go mod edit -json`
#   nextjs                    frontend/package.json
#   postgresql                the server image in backend/deploy/docker-compose.yaml
#
# .github/workflows/badges.yaml publishes the output to the `badges` branch,
# where the README's shields.io badges read it.

set -euo pipefail

root="${BADGE_ROOT:-$(git rev-parse --show-toplevel)}"
cd "$root"

readonly GO_MOD=backend/go.mod
readonly PACKAGE_JSON=frontend/package.json
readonly COMPOSE=backend/deploy/docker-compose.yaml
readonly MOD_CONNECT=connectrpc.com/connect/v2
readonly MOD_CEDAR=github.com/cedar-policy/cedar-go
readonly MOD_MCP=github.com/modelcontextprotocol/go-sdk
# The image Paladin's own stack runs; "postgres:18.6" → "18.6", and a digest
# Renovate pins after the tag ("postgres:18@sha256:…") is not part of it.
readonly POSTGRES_IMAGE=postgres

for tool in go jq yq; do
    command -v "$tool" >/dev/null 2>&1 || { echo "!!! $tool is not installed" >&2; exit 1; }
done

mod=$(go mod edit -json "$GO_MOD")
require() { # require <module path>: its version without the leading v
    jq -er --arg p "$1" '.Require[] | select(.Path == $p) | .Version | ltrimstr("v")' <<<"$mod"
}

# Assigned one by one: set -e stops on a failed assignment, not on a failed
# substitution inside another command's arguments.
go_version=$(jq -er '.Go' <<<"$mod")
connect=$(require "$MOD_CONNECT")
cedar=$(require "$MOD_CEDAR")
mcp=$(require "$MOD_MCP")
nextjs=$(jq -er '.dependencies.next | ltrimstr("^") | ltrimstr("~")' "$PACKAGE_JSON")
postgres=$(yq --yaml-fix-merge-anchor-to-spec=true -e ".services[] | select(.image | test(\"^${POSTGRES_IMAGE}:\")) | .image" "$COMPOSE" |
    head -1 | sed -e "s/^${POSTGRES_IMAGE}://" -e 's/@.*//')

jq -n \
    --arg go "$go_version" \
    --arg postgresql "$postgres" \
    --arg connect "$connect" \
    --arg cedar "$cedar" \
    --arg mcp "$mcp" \
    --arg nextjs "$nextjs" \
    '{go: $go, postgresql: $postgresql, connect: $connect, cedar: $cedar, mcp: $mcp, nextjs: $nextjs}'
