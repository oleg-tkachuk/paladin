#!/usr/bin/env bash
# proto-breaking.sh — refuse a wire-breaking proto change that nobody meant.
#
# Pre-1.0 this project still breaks compatibility deliberately (see
# docs/upgrading.md, "Changing the API contract"). This does not forbid that.
# It forbids doing it by accident — the failure mode that already happened:
# forty-five breaking changes accumulated in the four days after the paladin
# rename, most of them field renames that keep the field number. gRPC clients
# survive those. JSON clients do not. Nothing said so.
#
# Why it lives here and not only in CI: the check has existed in
# .github/workflows/test.yml since it was written and has NEVER EXECUTED —
# Actions refuses to start jobs on this account. A guard that only exists in a
# pipeline nobody can run is not a guard, which is the lesson the rest of this
# repository's gates were rebuilt around. Seconds, no Docker, so it belongs in
# the fast gate.
#
# The baseline tag is READ FROM THE WORKFLOW rather than repeated here. Two
# places that must agree about one string is how the port defaults and the
# plane URLs drifted; the workflow is the one an operator bumps when cutting a
# new baseline, so it is the source and this follows it.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

workflow=.github/workflows/test.yml

if ! command -v buf >/dev/null 2>&1; then
    {
        echo "!!! buf is not installed, and this gate does not skip."
        echo "    A silently-skipped compatibility check reads exactly like a"
        echo "    passing one, which is the state this gate exists to end."
        echo
        echo "      brew install bufbuild/buf/buf"
    } >&2
    exit 1
fi

baseline=$(sed -n 's/.*breaking_against:.*tag=\([^,]*\),.*/\1/p' "$workflow" | head -1)
if [[ -z "$baseline" ]]; then
    {
        echo "!!! could not read the API baseline tag from $workflow"
        echo "    Expected a line of the form:"
        echo "      breaking_against: \".git#tag=api/vX.Y.Z,subdir=backend/proto\""
        echo "    If that step was renamed or removed, this gate has nothing to"
        echo "    compare against and must not pretend otherwise."
    } >&2
    exit 1
fi

if ! git rev-parse --verify --quiet "refs/tags/$baseline" >/dev/null; then
    {
        echo "!!! the API baseline tag $baseline is not in this clone."
        echo "    A shallow or tagless fetch cannot run this check. Fetch it:"
        echo
        echo "      git fetch --tags origin"
    } >&2
    exit 1
fi

echo ">>> [proto] buf breaking against $baseline"
buf breaking backend/proto --against ".git#tag=$baseline,subdir=backend/proto"
echo ">>> [proto] wire contract unchanged since $baseline"
