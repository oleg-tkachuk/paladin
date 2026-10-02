#!/usr/bin/env bash
# badge-versions.test.sh — badge-versions.sh reads each version from the file
# that pins it, and fails rather than publish a blank badge.

set -euo pipefail

script="$(git rev-parse --show-toplevel)/scripts/badge-versions.sh"
work=$(mktemp -d "${TMPDIR:-/tmp}/badge-versions.XXXXXX")
trap 'rm -rf "$work"' EXIT

failed=0
fail() { printf 'FAIL %s\n' "$1"; failed=1; }

fixture() { # fixture <with the cedar module: yes|no>
    rm -rf "${work:?}"/*
    mkdir -p "$work/backend/deploy" "$work/frontend"
    {
        printf 'module example.com/fixture\n\ngo 1.30.1\n\nrequire (\n'
        printf '\tconnectrpc.com/connect v1.99.0\n'
        [[ "$1" == yes ]] && printf '\tgithub.com/cedar-policy/cedar-go v1.9.1\n'
        printf '\tgithub.com/modelcontextprotocol/go-sdk v1.10.2\n)\n'
    } >"$work/backend/go.mod"
    printf '{"dependencies": {"next": "^17.0.1"}}\n' >"$work/frontend/package.json"
    printf 'services:\n  cache:\n    image: redis:8\n  postgres:\n    image: postgres:18\n' \
        >"$work/backend/deploy/docker-compose.yaml"
}

fixture yes
got=$(BADGE_ROOT="$work" "$script" 2>/dev/null | jq -c .)
want='{"go":"1.30.1","postgresql":"18","connect":"1.99.0","cedar":"1.9.1","mcp":"1.10.2","nextjs":"17.0.1"}'
[[ "$got" == "$want" ]] || fail "versions: got $got, want $want"

fixture no
if BADGE_ROOT="$work" "$script" >/dev/null 2>&1; then
    fail "a module missing from go.mod published a blank badge"
fi

# And on this repository: every badge has a version.
"$(dirname "$script")/badge-versions.sh" 2>/dev/null |
    jq -e 'to_entries | all(.value | test("^[0-9]+(\\.[0-9]+)*$"))' >/dev/null ||
    fail "a badge version of this repository is not a version"

[[ $failed -eq 0 ]] || exit 1
echo "badge versions: each read from the file that pins it"
