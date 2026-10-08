#!/usr/bin/env bash
# badge-versions.test.sh — badge-versions.sh reads each version from the file
# that pins it, and fails rather than publish a blank badge.

set -euo pipefail

script="$(git rev-parse --show-toplevel)/scripts/badge-versions.sh"
work=$(mktemp -d "${TMPDIR:-/tmp}/badge-versions.XXXXXX")
trap 'rm -rf "$work"' EXIT

failed=0
fail() { printf 'FAIL %s\n' "$1"; failed=1; }

fixture() { # fixture <with the cedar module: yes|no> [postgres image tag]
    rm -rf "${work:?}"/*
    mkdir -p "$work/backend/deploy" "$work/frontend"
    {
        printf 'module example.com/fixture\n\ngo 1.30.1\n\nrequire (\n'
        printf '\tconnectrpc.com/connect/v2 v2.99.0\n'
        [[ "$1" == yes ]] && printf '\tgithub.com/cedar-policy/cedar-go v1.9.1\n'
        printf '\tgithub.com/modelcontextprotocol/go-sdk v1.10.2\n)\n'
    } >"$work/backend/go.mod"
    printf '{"dependencies": {"next": "^17.0.1"}}\n' >"$work/frontend/package.json"
    printf 'services:\n  cache:\n    image: redis:8\n  postgres:\n    image: postgres:%s\n' "${2:-18}" \
        >"$work/backend/deploy/docker-compose.yaml"
}

fixture yes
got=$(BADGE_ROOT="$work" "$script" 2>/dev/null | jq -c .)
want='{"go":"1.30.1","postgresql":"18","connect":"2.99.0","cedar":"1.9.1","mcp":"1.10.2","nextjs":"17.0.1"}'
[[ "$got" == "$want" ]] || fail "versions: got $got, want $want"

# Renovate pins a digest after the tag; the badge reads the version, not it.
fixture yes "18@sha256:74935e72241653ca55e0414067e6d8763aceb8a810eb51b452253ec3dcfc4336"
got=$(BADGE_ROOT="$work" "$script" 2>/dev/null | jq -r .postgresql)
[[ "$got" == 18 ]] || fail "a digest-pinned image: got postgresql $got, want 18"

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
