#!/usr/bin/env bash
# taskfile-forwarded-vars.test.sh — what an entry point sets, and what is set
# on its command line, must reach the component's task.
#
# An entry point starts a component's task as a child `task` process
# (`_component`), and a child inherits the environment but none of the parent's
# variables: neither a default the entry point declares nor a NAME=value given
# on its command line. `_component` hands a named few on. One it leaves out is
# dropped without a word — a chart pushed under a derived version instead of
# the pinned one, a cluster task run against the default context.
#
# Three things are held here:
#
#   1. K8S_CONTEXT as each entry point defaults it reaches both components'
#      cluster tasks. A component that declares the name itself — it was
#      declared in both for months — ignores what it is handed.
#   2. GLOBAL_REGISTRY, IMAGE_NAMESPACE and APP_VERSION given on the command
#      line reach the chart push, spelled as release.yaml spells it.
#   3. Every variable a workflow gives a component task on the command line is
#      one `_component` hands on.
#   4. The same for the repository's scripts: a script that needs another
#      variable calls the component's own Taskfile, where its command line
#      reaches the task. verify-stack.sh minted the SDK's data-plane token
#      through Taskfile.dev.yaml, and its JWT_AUD fell back to the admin plane.
#
# The child is made a dry run by a stub `task` first on PATH, so nothing here
# reaches a cluster or a registry.
#
# Runs from `task -t Taskfile.dev.yaml verify-repo` — no Docker, no cluster.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

readonly ENTRY_POINTS=(Taskfile.yaml Taskfile.local.yaml Taskfile.dev.yaml)
readonly COMPONENTS=(backend frontend)
# The task every component is started through, and the entry point the
# workflows run it from.
readonly COMPONENT_TASK=_component
readonly WORKFLOW_ENTRY=Taskfile.yaml
readonly WORKFLOWS=.github/workflows
# How a workflow names a component in a task: literally, or as its matrix's.
readonly WORKFLOW_COMPONENT='(backend|frontend|\$\{COMPONENT\}):'
# How a script starts a component task through an entry point: the entry
# point named after -t, then the component's task.
readonly SCRIPT_ENTRY_CALL='Taskfile(\.dev|\.local)?\.yaml"?[[:space:]]+(backend|frontend):'

readonly PROBE_CONTEXT=forwarded-context-probe
# The entry points' own declaration, which the copy rewrites.
readonly DECLARED='K8S_CONTEXT: '"'"'{{.K8S_CONTEXT | default "orbstack"}}'"'"
readonly PROBED='K8S_CONTEXT: '"'"'{{.K8S_CONTEXT | default "'"$PROBE_CONTEXT"'"}}'"'"
# A cluster task of each component, and how its dry run names the context.
readonly BACKEND_TASK=backend:seed:up
readonly BACKEND_WANT="ctx='$PROBE_CONTEXT'"
readonly FRONTEND_TASK=frontend:k8s:status
readonly FRONTEND_WANT="--context $PROBE_CONTEXT"

# The chart push, and how its dry run names what the command line gave it.
# .invalid resolves nowhere (RFC 2606).
readonly PUSH_TASK=release:chart:push
readonly PROBE_REGISTRY=registry.invalid
readonly PROBE_NAMESPACE=forwarded-namespace-probe
readonly PROBE_VERSION=0.0.0-forwarded.probe
readonly PUSH_WANT_TARGET="oci://$PROBE_REGISTRY/$PROBE_NAMESPACE/charts"
readonly PUSH_WANT_VERSION="--version $PROBE_VERSION"

command -v yq >/dev/null 2>&1 || {
    echo "!!! yq is not installed; the forwarded variables went unchecked — https://github.com/mikefarah/yq#install" >&2
    exit 1
}

failed=0
probe=""
work=$(mktemp -d "${TMPDIR:-/tmp}/taskfile-forwarded-vars-test.XXXXXX")
cleanup() {
    if [ -n "$probe" ]; then rm -f "$probe"; fi
    rm -rf "$work"
}
trap cleanup EXIT

# The stub the entry point's `task <target>` resolves to: the real Task, as a
# dry run with every variable resolved. --verbose because the components are
# `silent: true`, under which --dry prints nothing.
real_task=$(command -v task)
mkdir -p "$work/stub"
printf '#!/bin/sh\nexec %s --dry --verbose --offline "$@"\n' "$real_task" >"$work/stub/task"
chmod +x "$work/stub/task"

# The commands a component task would run when started through an entry point:
# the entry point for real, its child through the stub.
dry_run() {
    local entry=$1
    shift
    PATH="$work/stub:$PATH" "$real_task" -t "$entry" --offline "$@" </dev/null 2>&1 || true
}

# 1. Each entry point's own K8S_CONTEXT.
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

# 2. The command line, as release.yaml's chart push gives it.
for entry in "${ENTRY_POINTS[@]}"; do
    for component in "${COMPONENTS[@]}"; do
        out=$(dry_run "$entry" "$component:$PUSH_TASK" \
            GLOBAL_REGISTRY="$PROBE_REGISTRY" IMAGE_NAMESPACE="$PROBE_NAMESPACE" APP_VERSION="$PROBE_VERSION")
        for want in "$PUSH_WANT_TARGET" "$PUSH_WANT_VERSION"; do
            if ! grep -qF -- "$want" <<<"$out"; then
                echo "FAIL $entry: $component:$PUSH_TASK does not run with the command line's variables ($want)" >&2
                failed=1
            fi
        done
    done
done

# 3. What the workflows give a component task. A step's continuation lines are
# joined first; the names are the NAME= words after the task.
forwarded=$(yq -r ".tasks.$COMPONENT_TASK.cmd // \"\"" "$WORKFLOW_ENTRY" |
    grep -oE '(^|[[:space:]}])[A-Z][A-Z0-9_]*=' | grep -oE '[A-Z][A-Z0-9_]*' | sort -u || true)
given=$(cat "$WORKFLOWS"/*.yaml |
    awk '{ if (sub(/\\$/, "")) { held = held $0; next } print held $0; held = "" }' |
    grep -E "(^|[[:space:];&|(])task[[:space:]].*$WORKFLOW_COMPONENT" |
    sed -E "s/.*$WORKFLOW_COMPONENT//" |
    grep -oE '[[:space:]][A-Z][A-Z0-9_]*=' | grep -oE '[A-Z][A-Z0-9_]*' | sort -u || true)
if [ -z "$forwarded" ] || [ -z "$given" ]; then
    echo "!!! no variable read from $WORKFLOW_ENTRY's $COMPONENT_TASK or from the workflows' component tasks — this check would assert nothing" >&2
    failed=1
fi
dropped=$(comm -23 <(printf '%s\n' "$given") <(printf '%s\n' "$forwarded"))
if [ -n "$dropped" ]; then
    echo "FAIL a workflow gives a component task a variable $COMPONENT_TASK does not hand on: $(echo $dropped)" >&2
    failed=1
fi

# 4. What the repository's scripts give a component task through an entry
# point, read as the workflows are.
script_calls=$(git ls-files '*.sh' | while read -r script; do
    awk '{ if (sub(/\\$/, "")) { held = held $0; next } print held $0; held = "" }' "$script" |
        grep -E "(^|[[:space:];&|(])task[[:space:]].*$SCRIPT_ENTRY_CALL" |
        sed -E "s/^/$(printf '%s' "$script" | sed 's/[\/&]/\\&/g'): /" || true
done)
while IFS= read -r call; do
    [ -n "$call" ] || continue
    script=${call%%: *}
    names=$(sed -E "s/.*$SCRIPT_ENTRY_CALL//" <<<"$call" |
        grep -oE '[[:space:]][A-Z][A-Z0-9_]*=' | grep -oE '[A-Z][A-Z0-9_]*' | sort -u || true)
    lost=$(comm -23 <(printf '%s\n' "$names" | sed '/^$/d') <(printf '%s\n' "$forwarded"))
    if [ -n "$lost" ]; then
        echo "FAIL $script gives a component task, through an entry point, a variable $COMPONENT_TASK does not hand on: $(echo $lost)" >&2
        failed=1
    fi
done <<<"$script_calls"

if [ "$failed" -ne 0 ]; then
    echo "!!! a variable stops at the entry point: hand it on in $COMPONENT_TASK, in every entry point, and leave it undeclared in the components" >&2
    exit 1
fi
echo "taskfiles: every entry point's K8S_CONTEXT and the command line's $(echo $forwarded) reach both components"
