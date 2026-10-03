#!/usr/bin/env bash
# proto-breaking.sh — refuse a wire-breaking proto change that nobody meant.
#
# Pre-1.0 this project still breaks compatibility deliberately (see
# docs/upgrading.md, "Changing the API contract"). This does not forbid that.
# It forbids doing it by accident — the failure mode that already happened:
# forty-five breaking changes accumulated in four days, most of them field
# renames that keep the field number. gRPC clients survive those. JSON clients
# do not. Nothing said so.
#
# Why it lives here and not only in CI: it ran for months in a workflow that
# Actions never started on this account. A guard that only exists in a
# pipeline nobody can run is not a guard, which is the lesson the rest of this
# repository's gates were rebuilt around. Seconds, no Docker, so it belongs in
# the fast gate — which CI now runs as `task -t Taskfile.dev.yaml verify-all`.
#
# The baseline tag lives HERE and nowhere else. It used to be read out of the
# old test workflow; that workflow is gone, and this script is the only thing
# that compares against it. Bump it when cutting a new baseline — see
# docs/upgrading.md, "Changing the API contract".

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

# The published API contract this tree must stay wire-compatible with, and
# where that tag keeps its protos. Baselines up to api/v0.10.0 kept them in
# backend/proto; from api/v0.11.0 the contract lives at the repository root.
API_BASELINE_TAG=api/v0.14.0
API_BASELINE_SUBDIR=proto

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

if ! git rev-parse --verify --quiet "refs/tags/$API_BASELINE_TAG" >/dev/null; then
    {
        echo "!!! the API baseline tag $API_BASELINE_TAG is not in this clone."
        echo "    A shallow or tagless fetch cannot run this check. Fetch it:"
        echo
        echo "      git fetch --tags origin"
    } >&2
    exit 1
fi

echo ">>> [proto] buf breaking against $API_BASELINE_TAG"
buf breaking proto --against ".git#tag=$API_BASELINE_TAG,subdir=$API_BASELINE_SUBDIR"
echo ">>> [proto] wire contract unchanged since $API_BASELINE_TAG"
