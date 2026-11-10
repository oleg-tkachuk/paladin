#!/usr/bin/env bash
# taskfile-vars.test.sh — no entry point includes a component's Taskfile.
#
# Task keeps one pool for the top-level `vars:` of every Taskfile a root
# includes by path. backend and frontend each declare PROJECT_NAME,
# DOCKER_CONTEXT, CHART_MIN_RESOURCES and the helm flags, with different
# values, and included side by side a library module of one read the other's
# in a share of its runs: from Taskfile.dev.yaml, `backend:go:fmt:check`
# printed the console's name in 9 runs of 16. The same read decides the image,
# the chart and the docker context a release task acts with. Earlier, with
# DOCKER_CONTEXT declared by backend alone, the console image was built with
# the repository root as its context from anywhere but frontend/.
#
# Declaring every name on both sides did not close it, so the entry points do
# not include the components at all: each `<component>:*` task starts `task`
# in the component's directory, and a process of its own has a pool of its
# own. This refuses the include coming back. taskfile-drift.test.sh holds the
# tasks that replaced it.
#
# Runs from `task -t Taskfile.dev.yaml verify-repo` — no Docker, no cluster.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

readonly ENTRY_POINTS=(Taskfile.yaml Taskfile.local.yaml Taskfile.dev.yaml)
# What every include of an entry point is addressed through: the task library.
# Anything else names a file of this repository. Read by yq, through strenv.
readonly LIBRARY_VAR=TASKLIB
export LIBRARY_VAR

command -v yq >/dev/null 2>&1 || {
    echo "!!! yq is not installed; the Taskfile var contract went unchecked — https://github.com/mikefarah/yq#install" >&2
    exit 1
}

# An include is a mapping with `taskfile:`, or the path alone.
local_includes_of() {
    yq -r '
        (.includes // {})
        | to_entries
        | map(select((.value.taskfile // .value // "") | tostring | test(strenv(LIBRARY_VAR)) | not))
        | .[]
        | .key + " (" + ((.value.taskfile // .value) | tostring) + ")"
    ' "$1"
}

# The reader itself first, on both spellings of a local include beside a
# library one: reading nothing out of every file would otherwise pass.
fixture=$(mktemp "${TMPDIR:-/tmp}/taskfile-vars-test.XXXXXX")
trap 'rm -f "$fixture"' EXIT
cat >"$fixture" <<'FIXTURE'
includes:
  mapped: { taskfile: backend/Taskfile.yaml, dir: backend }
  short: ./frontend
  library: { taskfile: '{{printf .TASKLIB "go"}}', dir: . }
FIXTURE
readonly FIXTURE_WANT=$'mapped (backend/Taskfile.yaml)\nshort (./frontend)'
if [[ "$(local_includes_of "$fixture")" != "$FIXTURE_WANT" ]]; then
    echo "!!! the include reader no longer tells a local include from a library one — this check is asserting nothing" >&2
    exit 1
fi

failed=0
for entry in "${ENTRY_POINTS[@]}"; do
    # yq's own failure must not read as "nothing included".
    if ! found=$(local_includes_of "$entry"); then
        echo "!!! $entry could not be read — this check is asserting nothing" >&2
        exit 1
    fi
    if [[ -n "$found" ]]; then
        {
            echo "!!! $entry includes a Taskfile of this repository:"
            printf '%s\n' "$found" | sed 's/^/      /'
            echo "      Its top-level vars would share one pool with every other include's."
            echo "      Run it as a child process instead, as the <component>:* tasks do."
        } >&2
        failed=1
    fi
done

[[ "$failed" -eq 0 ]] || exit 1

echo "taskfiles: none of the ${#ENTRY_POINTS[@]} entry points includes a component's Taskfile"
