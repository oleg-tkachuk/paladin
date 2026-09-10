#!/usr/bin/env bash
# taskfile-vars.test.sh — the component Taskfiles must declare the same set of
# variable names.
#
# Not a tidiness rule. Task does not isolate an included file's `vars:` from
# its siblings: a variable one component leaves unset falls through to whatever
# another component declared. backend set DOCKER_CONTEXT to `..` and frontend
# set nothing, so from the repo root `task frontend:release:image:build` built
# the console with the REPO ROOT as its docker context and died on a COPY of
# frontend/pnpm-workspace.yaml — while the identical task run from frontend/
# succeeded, because there was no sibling to inherit from. That took a while to
# find, and `task verify-e2e` was broken by it the whole time.
#
# Identical NAMES, not identical values: each component still sets its own
# PROJECT_NAME and its own context. What this refuses is a name declared on one
# side only, because that is exactly the gap the fall-through fills.
#
# Runs from `task verify-all` — no Docker, no cluster.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

command -v yq >/dev/null 2>&1 || {
    echo "!!! yq is not installed; the Taskfile var contract went unchecked — https://github.com/mikefarah/yq#install" >&2
    exit 1
}

# `// {}` and `|| true`, and not for tidiness: a renamed or deleted `vars:`
# block makes yq exit non-zero, and under `set -e` the script would then die on
# yq's own message instead of reaching the empty-set guard below — failing, but
# saying nothing useful about why.
names_of() { yq -r '(.vars // {}) | keys | .[]' "$1" 2>/dev/null | sort || true; }

backend=$(names_of backend/Taskfile.yaml)
frontend=$(names_of frontend/Taskfile.yaml)

# A rename on either side would otherwise empty a set and make the comparison
# below trivially true.
for side in backend frontend; do
    if [[ -z "${!side}" ]]; then
        echo "!!! $side/Taskfile.yaml declares no vars — this check is asserting nothing" >&2
        exit 1
    fi
done

only_backend=$(comm -23 <(printf '%s\n' "$backend") <(printf '%s\n' "$frontend"))
only_frontend=$(comm -13 <(printf '%s\n' "$backend") <(printf '%s\n' "$frontend"))

if [[ -n "$only_backend" || -n "$only_frontend" ]]; then
    {
        echo "!!! the component Taskfiles do not declare the same variables:"
        [[ -n "$only_backend" ]] && printf '      backend only:  %s\n' $only_backend
        [[ -n "$only_frontend" ]] && printf '      frontend only: %s\n' $only_frontend
        echo "      A name set on one side only is inherited by the other from the"
        echo "      repo root. Declare it on both, or drop it from the one that has it."
    } >&2
    exit 1
fi

echo "taskfiles: backend and frontend declare the same $(printf '%s\n' "$backend" | wc -l | tr -d ' ') variables"
