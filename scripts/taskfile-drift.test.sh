#!/usr/bin/env bash
# taskfile-drift.test.sh — the entry points must reach the same components, in
# the same trees, handing on the same variables.
#
# Taskfile.yaml is what an operator runs, Taskfile.local.yaml publishes to the
# host-only registry and Taskfile.dev.yaml is what a developer runs; all three
# need `backend:` and `frontend:` to resolve. Each carries its own copy of the
# tasks that start a component as a child process: `_component`, and one
# `<component>:*` beside it.
#
# Copies are the cheaper problem, but only while something notices them
# parting. Nothing would: each entry point is correct on its own, and the
# failure is a component that deploys from one file and is tested from another
# against a different directory.
#
# Compares the component tasks whole — name, directory and the call — AND
# `_component`, the one place a variable is handed to the child: a
# GLOBAL_REGISTRY forwarded from one entry point and not another is a deploy
# that silently goes somewhere else.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no Docker, no cluster.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

# The first is the reference the others are compared against.
entry_points=(Taskfile.yaml Taskfile.local.yaml Taskfile.dev.yaml)

command -v yq >/dev/null 2>&1 || {
    echo "!!! yq is not installed; the Taskfile component contract went unchecked — https://github.com/mikefarah/yq#install" >&2
    exit 1
}

# The task every component is started through, and the name of the variable
# that carries the component's directory to it.
readonly COMPONENT_TASK=_component
readonly COMPONENT_DIR_VAR=COMPONENT
# Read by the yq programs below, through strenv.
export COMPONENT_TASK COMPONENT_DIR_VAR

# One line per component task — a wildcard task whose only command calls
# COMPONENT_TASK — and one for COMPONENT_TASK itself. Everything else in an
# entry point is its own business.
components_of() {
    yq -r '
        .tasks
        | to_entries
        | map(select(.key == strenv(COMPONENT_TASK)
                     or ((.value.cmds // [])[0].task // "") == strenv(COMPONENT_TASK)))
        | sort_by(.key)
        | .[]
        | .key + "  " + (.value | to_json(0))
    ' "$1"
}

# The directories the component tasks name.
dirs_of() {
    yq -r '
        .tasks
        | to_entries
        | map(select(((.value.cmds // [])[0].task // "") == strenv(COMPONENT_TASK)))
        | .[].value.cmds[0].vars[strenv(COMPONENT_DIR_VAR)]
    ' "$1" | sort
}

reference=${entry_points[0]}
reference_components=$(components_of "$reference")

# A renamed COMPONENT_TASK, or a component task that stopped calling it, would
# otherwise empty one side and make the diff below trivially clean.
for file in "${entry_points[@]}"; do
    if [[ -z "$(dirs_of "$file")" ]]; then
        echo "!!! $file declares no component tasks — this check is asserting nothing" >&2
        exit 1
    fi
    for dir in $(dirs_of "$file"); do
        if [[ ! -f "$dir/Taskfile.yaml" ]]; then
            echo "!!! $file names the component $dir, which has no Taskfile.yaml" >&2
            exit 1
        fi
    done
done

for file in "${entry_points[@]:1}"; do
    if ! diff -u <(printf '%s\n' "$reference_components") <(components_of "$file") \
        --label "$reference" --label "$file" >/dev/null; then
        {
            echo "!!! $reference and $file disagree about the components:"
            diff -u <(printf '%s\n' "$reference_components") <(components_of "$file") \
                --label "$reference" --label "$file" | sed 's/^/      /'
            echo "      Every entry point must declare the same component tasks."
            echo "      Copy them from whichever one is right into the others."
        } >&2
        exit 1
    fi
done

echo "taskfiles: ${#entry_points[@]} entry points reach the same $(dirs_of "$reference" | wc -l | tr -d ' ') components"
