#!/usr/bin/env bash
# taskfile-forwarded-vars.test.sh — the K8S_CONTEXT an entry point sets must
# reach the components' cluster tasks.
#
# Task 3.53.1 evaluates an included Taskfile's own `vars:` before the include's
# `vars:`, so a component that declares a name — even as
# `'{{.X | default "..."}}'` — silently ignores what the entry point forwards.
# K8S_CONTEXT was declared in both components for months, and every entry
# point's value was dropped; it went unnoticed only because every default was
# `orbstack`.
#
# For each entry point, a copy with a different default is run dry, and the
# commands the cluster tasks would run must name that context.
#
# Runs from `task -t Taskfile.dev.yaml verify-repo` — no Docker, no cluster.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

readonly ENTRY_POINTS=(Taskfile.yaml Taskfile.local.yaml Taskfile.dev.yaml)
readonly PROBE_CONTEXT=forwarded-context-probe
# The entry points' own declaration, which the copy rewrites.
readonly DECLARED='K8S_CONTEXT: '"'"'{{.K8S_CONTEXT | default "orbstack"}}'"'"
readonly PROBED='K8S_CONTEXT: '"'"'{{.K8S_CONTEXT | default "'"$PROBE_CONTEXT"'"}}'"'"
# A cluster task of each component, and how its dry run names the context.
readonly BACKEND_TASK=backend:seed:up
readonly BACKEND_WANT="ctx='$PROBE_CONTEXT'"
readonly FRONTEND_TASK=frontend:k8s:status
readonly FRONTEND_WANT="--context $PROBE_CONTEXT"

failed=0
probe=""
cleanup() { if [ -n "$probe" ]; then rm -f "$probe"; fi; }
trap cleanup EXIT

# The command a task would run, with every variable resolved. --verbose because
# the components are `silent: true`, under which --dry prints nothing.
dry_run() { task -t "$1" --dry --verbose --offline "$2" </dev/null 2>&1 || true; }

for entry in "${ENTRY_POINTS[@]}"; do
    if ! grep -qF "  $DECLARED" "$entry"; then
        echo "!!! $entry no longer declares $DECLARED — this check would assert nothing" >&2
        failed=1
        continue
    fi
    # Beside the original, so its includes and remote cache resolve the same.
    probe=".forwarded-vars-probe.$entry"
    sed "s#  $DECLARED#  $PROBED#" "$entry" >"$probe"
    for pair in "$BACKEND_TASK|$BACKEND_WANT" "$FRONTEND_TASK|$FRONTEND_WANT"; do
        task_name=${pair%%|*}
        want=${pair#*|}
        if ! dry_run "$probe" "$task_name" | grep -qF -- "$want"; then
            echo "FAIL $entry: $task_name does not run with the entry point's K8S_CONTEXT ($want)" >&2
            failed=1
        fi
    done
    rm -f "$probe"
    probe=""
done

if [ "$failed" -ne 0 ]; then
    echo "!!! a component declares K8S_CONTEXT itself; leave it undeclared and require it where it is used" >&2
    exit 1
fi
echo "taskfiles: every entry point's K8S_CONTEXT reaches both components' cluster tasks"
