#!/usr/bin/env bash
# backend-roles.test.sh — the backend's cluster tasks must act on the
# Deployments its chart renders.
#
# The chart runs one Deployment per role, <release>-<role>, and none named
# after the release itself. The library's k8s tasks default to PROJECT_NAME,
# so `task backend:restart` used to look for deploy/paladin-core, find
# nothing, and pass with a skip line — every restart a no-op. ROLES in
# backend/Taskfile.yaml now names each one, and two things are held here:
#
#   1. ROLES spells exactly the chart's roles — every key of `deployments`,
#      the ones disabled by default included — and each renders as the
#      Deployment ROLES names once enabled. A role added, dropped or renamed
#      in one place fails here, not as a silent skip.
#   2. restart and status reach every one of them, and logs the ROLE it is
#      given — read from a dry run, so nothing here reaches a cluster.
#
# Runs from `task -t Taskfile.dev.yaml verify-repo` — no Docker, no cluster.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

readonly COMPONENT=backend
readonly TASKFILE="$COMPONENT/Taskfile.yaml"
readonly CHART="$COMPONENT/deploy/chart"
readonly REQUIRED_VALUES="$CHART/ci/required-values.yaml"
# The release name the clusters install the chart under; the chart names each
# Deployment after it.
readonly RELEASE=paladin-core
# A context nothing resolves: the dry run prints commands, it never runs them.
readonly PROBE_CONTEXT=backend-roles-probe
# The role logs is pointed at below, and what each task's dry run prints for
# the Deployment it acts on.
readonly PROBE_ROLE=iam
readonly RESTART_VERB='rollout restart deploy/'
readonly STATUS_VERB='get deploy/'
readonly LOGS_VERB='logs deploy/'

for tool in yq helm task; do
    command -v "$tool" >/dev/null 2>&1 || {
        echo "!!! $tool is not installed; the backend's roles went unchecked" >&2
        exit 1
    }
done

project=$(yq -r '.vars.PROJECT_NAME' "$TASKFILE")
# Role names are single words, so word splitting is the parse.
# shellcheck disable=SC2207
roles=($(yq -r '.vars.ROLES[]' "$TASKFILE"))
if [ "${#roles[@]}" -eq 0 ]; then
    echo "!!! $TASKFILE declares no ROLES — this check is asserting nothing" >&2
    exit 1
fi

failed=0
fail() { echo "FAIL $*" >&2; failed=1; }

want=$(for r in "${roles[@]}"; do echo "$project-$r"; done | sort)

# 1. The chart's roles, and the Deployments they render with all enabled.
chart_roles=$(yq -r '.deployments | keys | .[]' "$CHART/values.yaml" | sort)
if [ "$(printf '%s\n' "${roles[@]}" | sort)" != "$chart_roles" ]; then
    fail "ROLES in $TASKFILE and the keys of deployments in $CHART/values.yaml differ:"
    diff <(printf '%s\n' "${roles[@]}" | sort) <(echo "$chart_roles") | sed 's/^/      /' >&2 || true
fi
enable_all=()
for r in $chart_roles; do enable_all+=(--set "deployments.$r.enabled=true"); done
rendered=$(helm template "$RELEASE" "$CHART" -f "$REQUIRED_VALUES" "${enable_all[@]}" |
    yq -r 'select(.kind == "Deployment") | .metadata.name' | grep -v '^---$' | sort)
if [ "$want" != "$rendered" ]; then
    fail "ROLES in $TASKFILE and the Deployments the chart renders with every role enabled differ:"
    diff <(echo "$want") <(echo "$rendered") | sed 's/^/      /' >&2 || true
fi

# 2. What each task would act on.
dry() {
    K8S_CONTEXT="$PROBE_CONTEXT" task -d "$COMPONENT" --dry --verbose --offline "$@" </dev/null 2>&1
}
targets() { grep -o "$1[A-Za-z0-9.-]*" | sed "s|^$1||" | sort -u; }

for case in "restart:$RESTART_VERB" "status:$STATUS_VERB"; do
    task_name=${case%%:*}
    verb=${case#*:}
    got=$(dry "$task_name" | targets "$verb")
    if [ "$want" != "$got" ]; then
        fail "task $COMPONENT:$task_name acts on [${got//$'\n'/ }], want [${want//$'\n'/ }]"
    fi
done

got=$(dry logs ROLE="$PROBE_ROLE" | targets "$LOGS_VERB")
if [ "$got" != "$project-$PROBE_ROLE" ]; then
    fail "task $COMPONENT:logs ROLE=$PROBE_ROLE follows [${got//$'\n'/ }], want [$project-$PROBE_ROLE]"
fi

if [ "$failed" -ne 0 ]; then exit 1; fi
echo "backend roles: ${#roles[@]} Deployments, the same in the chart and in restart, status and logs"
