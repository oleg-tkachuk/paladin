#!/usr/bin/env bash
# taskfile-drift.test.sh — the entry points must declare the same components,
# pointed at the same trees, with the same variables forwarded.
#
# Taskfile.yaml is what an operator runs, Taskfile.local.yaml publishes to the
# host-only registry and Taskfile.dev.yaml is what a developer runs; all three
# need `backend:` and `frontend:` to resolve. The include blocks are therefore
# duplicated rather than shared: a shared file included by all of them would
# have to be flattened to keep the namespaces spelled `backend:deploy` instead
# of `components:backend:deploy`, and Task resolves an included file's vars out
# of one pool shared with its siblings — the same leak taskfile-vars.test.sh
# exists for, made worse by another level of nesting.
#
# Copies are the cheaper problem, but only while something notices them
# parting. Nothing would: each entry point is correct on its own, and the
# failure is a component that deploys from one file and is tested from another
# against a different directory.
#
# Compares name, taskfile, dir AND the forwarded vars. Names alone would pass a
# component repointed at another tree in one of them; vars matter because a
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

# The library modules are addressed through TASKLIB and are each entry point's
# own business — the fan-out deliberately carries different `excludes` in
# each. What has to match is the components, which are the includes naming a
# path in this repository.
components_of() {
    yq -r '
        .includes
        | to_entries
        | map(select((.value.taskfile // "") | test("TASKLIB") | not))
        | sort_by(.key)
        | .[]
        | .key + "  " + (.value.taskfile // "<no taskfile>")
              + "  dir=" + (.value.dir // "<no dir>")
              + "  vars=" + ((.value.vars // {}) | to_entries | sort_by(.key)
                             | map(.key + "=" + .value) | join(","))
    ' "$1"
}

reference=${entry_points[0]}
reference_components=$(components_of "$reference")

# A renamed `includes:` key, or a component switched to a TASKLIB path, would
# otherwise empty one side and make the diff below trivially clean.
for file in "${entry_points[@]}"; do
    if [[ -z "$(components_of "$file")" ]]; then
        echo "!!! $file declares no component includes — this check is asserting nothing" >&2
        exit 1
    fi
done

for file in "${entry_points[@]:1}"; do
    if ! diff -u <(printf '%s\n' "$reference_components") <(components_of "$file") \
        --label "$reference" --label "$file" >/dev/null; then
        {
            echo "!!! $reference and $file disagree about the components:"
            diff -u <(printf '%s\n' "$reference_components") <(components_of "$file") \
                --label "$reference" --label "$file" | sed 's/^/      /'
            echo "      Every entry point must declare the same component includes."
            echo "      Copy the block from whichever one is right into the others."
        } >&2
        exit 1
    fi
done

echo "taskfiles: ${#entry_points[@]} entry points declare the same $(printf '%s\n' "$reference_components" | wc -l | tr -d ' ') components"
