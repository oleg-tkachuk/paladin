#!/usr/bin/env bash
# workflows-audit.sh — the two checks CI runs over .github/workflows, here.
#
# actionlint (is each workflow valid at all: schema, expressions, `needs:`,
# and every `run:` block through shellcheck) and zizmor (security: unpinned actions,
# injectable expressions, leaked credentials). They ran only as CI jobs, so a
# workflow change was first checked after it was pushed.
#
# The versions are read from ci.yaml's env block, the one place CI pins them,
# so this cannot drift from what CI enforces. zizmor's online audits use a
# GitHub token when `gh` has one; without it they are skipped, as in CI with
# no token.
#
# Runs as `task -t Taskfile.dev.yaml verify:workflows`. Needs go and pipx.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

readonly CI_WORKFLOW=.github/workflows/ci.yaml
readonly ZIZMOR_CONFIG=.github/zizmor.yml
readonly WORKFLOWS_DIR=.github/workflows/

for tool in go pipx yq; do
    command -v "$tool" >/dev/null 2>&1 || {
        echo "!!! $tool is not installed; the workflows went unchecked" >&2
        exit 1
    }
done

pin() {
    local v
    v=$(yq -r ".env.$1" "$CI_WORKFLOW")
    if [ -z "$v" ] || [ "$v" = null ]; then
        echo "!!! $CI_WORKFLOW pins no $1" >&2
        exit 1
    fi
    printf '%s\n' "$v"
}
actionlint_version=$(pin ACTIONLINT_VERSION)
zizmor_version=$(pin ZIZMOR_VERSION)

echo ">>> actionlint ${actionlint_version}"
go run "github.com/rhysd/actionlint/cmd/actionlint@${actionlint_version}"

echo ">>> zizmor ${zizmor_version}"
token=$(gh auth token 2>/dev/null || true)
GH_TOKEN="$token" pipx run "zizmor==${zizmor_version}" \
    --config "$ZIZMOR_CONFIG" --persona=regular --format=plain "$WORKFLOWS_DIR"

echo "workflows: actionlint and zizmor pass at CI's pinned versions"
