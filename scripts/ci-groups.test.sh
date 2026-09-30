#!/usr/bin/env bash
# ci-groups.test.sh — the Verify groups a change reaches, per scripts/ci-groups.sh.
#
# Too few groups and a change merges on checks that never ran against it; too
# many and the split buys nothing. Both are silent in CI, so each case below
# pins one decision.
#
# Runs from `task -t Taskfile.dev.yaml verify-repo` — no network.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
script="$root/scripts/ci-groups.sh"
failed=0

check() {
    local name=$1 want=$2 got
    shift 2
    got="$(printf '%s\n' "$@" | "$script")"
    if [ "$got" != "$want" ]; then
        printf 'FAIL %s\n  want %s\n  got  %s\n' "$name" "$want" "$got"
        failed=1
    fi
}

all='["backend","capability","sdk","frontend","repo"]'

check "documentation only"        '[]' README.md docs/install.md backend/README.md docs/diagram.svg
check "nothing changed"           '[]'
check "gitignore and licence"     '[]' .gitignore LICENSE NOTICE
check "a console file"            '["frontend","repo"]' frontend/src/app/page.tsx
check "a backend file"            '["backend","repo"]' backend/internal/mcp/bridge.go
check "capability reaches backend" '["backend","capability","repo"]' capability/token.go
check "the Go SDK reaches backend" '["backend","sdk","repo"]' sdk/go/client.go
check "the Python SDK only"       '["sdk","repo"]' sdk/python/pyproject.toml
check "the contract reaches all its readers" '["backend","sdk","frontend","repo"]' proto/paladin/admin/v1/tenant_service.proto
check "a chart"                   '["frontend","repo"]' frontend/deploy/chart/values.yaml
check "root config"               '["repo"]' .checkov.yaml
check "an unclassified path"      '["repo"]' deploy/grafana/dashboard.json
check "a Taskfile"                "$all" Taskfile.dev.yaml
check "a cached include"          "$all" .task/remote/git.github.com.codegen.yaml
check "a contract script"         "$all" scripts/chart-values.test.sh
check "this workflow"             "$all" .github/workflows/ci.yaml
check "another workflow"          '["repo"]' .github/workflows/release.yaml
got_all="$("$script" --all </dev/null)"
if [ "$got_all" != "$all" ]; then
    printf 'FAIL --all\n  want %s\n  got  %s\n' "$all" "$got_all"
    failed=1
fi
check "docs next to code"         '["frontend","repo"]' README.md frontend/src/app/page.tsx

if [ "$failed" -ne 0 ]; then
    exit 1
fi
printf '%s\n' "ci groups: every case matches"
