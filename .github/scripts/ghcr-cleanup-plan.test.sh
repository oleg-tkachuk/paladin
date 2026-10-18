#!/usr/bin/env bash
# ghcr-cleanup-plan.test.sh — a dev publish deletes older dev builds and
# nothing else, keeping the newest.
#
# What it matches is the whole safety of the step: a pattern that also caught
# a release, or a cosign tag, would delete it with nothing to warn of it. So
# the plan's pattern is matched here against every shape of tag GHCR holds,
# and the workflow is read to see that it runs the plan only for a dev build
# and hands the action exactly what the plan printed.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no network.

set -euo pipefail

root="$(git rev-parse --show-toplevel)"
script="$root/.github/scripts/ghcr-cleanup-plan.sh"
workflow="$root/.github/workflows/release.yaml"

failures=0
fail() {
    printf 'FAIL %s\n' "$1"
    failures=$((failures + 1))
}

plan="$("$script" paladin-core charts/paladin-core)"
out() { printf '%s\n' "$plan" | sed -n "s/^$1=//p"; }

[ "$(out packages)" = "paladin-core,charts/paladin-core" ] || fail "packages: $(out packages)"
[ "$(out keep)" = "1" ] || fail "keeps $(out keep) dev builds, want the newest alone"

# The action matches tags with shell-style wildcards, as [[ == ]] does.
glob="$(out delete_tags)"
# shellcheck disable=SC2053 # unquoted on purpose: the glob is what is tested
matches() { [[ "$1" == $glob ]]; }
for tag in 38.1.1-dev.1792353836.gdab042fc 0.1.0-dev.1.gabcdef12; do
    matches "$tag" || fail "a dev build is not matched: $tag"
done
for tag in 38.1.0 38.1.1 v38.1.0 \
    sha256-eada85d13ca39a2939bf21479bfa2e6303f480a7ecd8f4997f3574eec93e2a12; do
    matches "$tag" && fail "matched what is not a dev build: $tag"
done

"$script" paladin-core 2>/dev/null && fail "accepted a missing chart package"

# The workflow: both steps only for a dev build, by the kind publish-plan.sh
# prints for one, and the action given the plan's outputs unchanged.
kind_dev="$(sed -n 's/^readonly KIND_DEV=//p' "$root/.github/scripts/publish-plan.sh" | tr -d '"')"
step() { yq -r ".jobs.publish.steps[] | select(.name == \"$1\") | $2" "$workflow"; }
for name in "Plan the cleanup of older dev builds" "Delete older dev builds"; do
    [ "$(step "$name" .if)" = "env.KIND == '$kind_dev'" ] || fail "$name runs for more than a dev build: $(step "$name" .if)"
done
for input in packages:packages delete-tags:delete_tags keep-n-tagged:keep dry-run:dry_run; do
    want="\${{ steps.cleanup.outputs.${input#*:} }}"
    got="$(step "Delete older dev builds" ".with[\"${input%%:*}\"]")"
    [ "$got" = "$want" ] || fail "${input%%:*} is $got, want $want"
done

if [ "$failures" -gt 0 ]; then
    exit 1
fi
printf '%s\n' "ghcr cleanup plan: older dev builds only, the newest kept"
