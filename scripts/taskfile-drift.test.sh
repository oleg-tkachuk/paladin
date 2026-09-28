#!/usr/bin/env bash
# taskfile-drift.test.sh — the two entry points must declare the same
# components, pointed at the same trees, with the same variables forwarded.
#
# Taskfile.yaml is what an operator runs and Taskfile.dev.yaml is what a
# developer runs, and both need `backend:` and `frontend:` to resolve. The
# include blocks are therefore duplicated rather than shared: a third file
# included by both would have to be flattened to keep the namespaces spelled
# `backend:deploy` instead of `components:backend:deploy`, and Task resolves an
# included file's vars out of one pool shared with its siblings — the same leak
# taskfile-vars.test.sh exists for, made worse by another level of nesting.
#
# Two copies are the cheaper problem, but only while something notices them
# parting. Nothing would: each entry point is correct on its own, and the
# failure is a component that deploys from one file and is tested from the
# other against a different directory.
#
# Compares name, taskfile, dir AND the forwarded vars. Names alone would pass a
# component repointed at another tree in one of the two; vars matter because a
# GLOBAL_REGISTRY forwarded from one entry point and not the other is a deploy
# that silently goes somewhere else.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no Docker, no cluster.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

ops=Taskfile.yaml
dev=Taskfile.dev.yaml

command -v yq >/dev/null 2>&1 || {
    echo "!!! yq is not installed; the Taskfile component contract went unchecked — https://github.com/mikefarah/yq#install" >&2
    exit 1
}

# The library modules are addressed through TASKLIB and are each entry point's
# own business — the fan-out deliberately carries different `excludes` on the
# two sides. What has to match is the components, which are the includes naming
# a path in this repository.
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

ops_components=$(components_of "$ops")
dev_components=$(components_of "$dev")

# A renamed `includes:` key, or a component switched to a TASKLIB path, would
# otherwise empty one side and make the diff below trivially clean.
for side in ops dev; do
    file_var="${side}"
    list_var="${side}_components"
    if [[ -z "${!list_var}" ]]; then
        echo "!!! ${!file_var} declares no component includes — this check is asserting nothing" >&2
        exit 1
    fi
done

if ! diff -u <(printf '%s\n' "$ops_components") <(printf '%s\n' "$dev_components") \
    --label "$ops" --label "$dev" >/dev/null; then
    {
        echo "!!! the two entry points disagree about the components:"
        diff -u <(printf '%s\n' "$ops_components") <(printf '%s\n' "$dev_components") \
            --label "$ops" --label "$dev" | sed 's/^/      /'
        echo "      Both files must declare the same component includes. Copy the"
        echo "      block from whichever one is right into the other."
    } >&2
    exit 1
fi

echo "taskfiles: both entry points declare the same $(printf '%s\n' "$ops_components" | wc -l | tr -d ' ') components"
