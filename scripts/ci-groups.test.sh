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

# The Playwright job runs for a change the console or the backend reaches, and
# for nothing else: it is the slowest job here, and it is the only one that
# exercises the two together.
check_e2e() {
    local name=$1 want=$2 groups=$3 got
    got="$(printf '%s' "$groups" | "$script" --needs-e2e)"
    if [ "$got" != "$want" ]; then
        printf 'FAIL e2e %s\n  want %s\n  got  %s\n' "$name" "$want" "$got"
        failed=1
    fi
}
check_e2e "a console change"      true  '["frontend","repo"]'
check_e2e "a backend change"      true  '["backend","capability","repo"]'
check_e2e "everything"            true  "$all"
check_e2e "the Python SDK only"   false '["sdk","repo"]'
check_e2e "repository config"     false '["repo"]'
check_e2e "documentation only"    false '[]'

# verify-deep runs for backend code, and for capability, the Go SDK and the
# contract only because those reach the backend group.
check_deep() {
    local name=$1 want=$2 groups=$3 got
    got="$(printf '%s' "$groups" | "$script" --needs-deep)"
    if [ "$got" != "$want" ]; then
        printf 'FAIL deep %s\n  want %s\n  got  %s\n' "$name" "$want" "$got"
        failed=1
    fi
}
check_deep "a backend change"     true  '["backend","repo"]'
check_deep "capability"           true  "$(printf '%s\n' capability/token.go | "$script")"
check_deep "everything"           true  "$all"
check_deep "a console change"     false '["frontend","repo"]'
check_deep "the Python SDK only"  false '["sdk","repo"]'
check_deep "documentation only"   false '[]'

# CodeQL skips the same documentation-only changes through paths-ignore, a
# glob list that cannot share INERT's regex. Every path this file treats as
# documentation must be ignored there, and no code path may be.
command -v yq >/dev/null 2>&1 || { echo "!!! yq is not installed" >&2; exit 1; }
codeql_ignore="$(yq -r 'explode(.) | .on.push."paths-ignore"[]' "$root/.github/workflows/codeql.yaml")"
python3 - "$codeql_ignore" <<'PY' || failed=1
import sys
from pathlib import PurePath
globs = sys.argv[1].split()
docs = ["README.md", "docs/install.md", "backend/README.md", "docs/diagram.svg",
        "backend/docs/cedar-authoring.md", ".gitignore", "LICENSE", "NOTICE", "frontend/public/logo.png"]
code = ["backend/internal/mcp/bridge.go", "frontend/src/app/page.tsx", "proto/paladin/admin/v1/tenant_service.proto",
        "deploy/grafana/paladin-alerts.yaml", ".github/workflows/codeql.yaml", "Taskfile.dev.yaml"]
ignored = lambda p: any(PurePath(p).full_match(g) for g in globs)
bad = [f"not ignored by CodeQL: {p}" for p in docs if not ignored(p)]
bad += [f"ignored by CodeQL: {p}" for p in code if ignored(p)]
for line in bad:
    print("FAIL codeql paths-ignore", line)
sys.exit(1 if bad else 0)
PY

if [ "$failed" -ne 0 ]; then
    exit 1
fi
printf '%s\n' "ci groups: every case matches"
