#!/usr/bin/env bash
# publish-target.test.sh — where release.yaml publishes, where Taskfile.yaml
# points, and what the charts pull by default must be the same place.
#
# The image path is spelled in three files that cannot import from one
# another: the workflow's env, the root Taskfile's defaults, and the image
# each chart renders. A chart whose default names a
# path nothing publishes to installs cleanly and then sits in ImagePullBackOff.
#
# Also checks that the chart path the workflow signs under is the one the task
# library pushes to: a mismatch signs nothing that was pushed, and fails only on
# release day.
#
# Also checks the workflow's component matrix against the components
# Taskfile.yaml reaches, so a third component is not added to the entry points
# and forgotten by the release.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no Docker, no network.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

readonly ROOT_TASKFILE=Taskfile.yaml
readonly WORKFLOW=.github/workflows/release.yaml
readonly RELEASE_CONFIG=.github/release/release.config.cjs
# The task every component is started through, and the name of the variable
# that carries the component's directory to it. Read by yq, through strenv.
readonly COMPONENT_TASK=_component
readonly COMPONENT_DIR_VAR=COMPONENT
export COMPONENT_TASK COMPONENT_DIR_VAR
# Where a directory keeps the task library modules its Taskfile includes.
readonly REMOTE_LOCK=.task/remote
readonly CHART_DIR=deploy/chart
# Any valid SemVer; it only has to come back out of the render unchanged.
readonly PROBE_VERSION=0.0.0-probe
# The inputs a chart cannot default, added to every render.
readonly REQUIRED_VALUES=ci/required-values.yaml
# What the workflow's IMAGE_NAMESPACE must be: the repository owner, which is
# only known to Actions, so it is compared against the owner .github/release/release.config.cjs
# names instead.
readonly OWNER_EXPRESSION='${{ github.repository_owner }}'

for tool in yq helm; do
    command -v "$tool" >/dev/null 2>&1 || {
        echo "!!! $tool is not installed; the publish target went unchecked" >&2
        exit 1
    }
done

fail=0
bad() { echo "!!! $*" >&2; fail=1; }

# The value inside `{{.NAME | default "value"}}`.
default_of() {
    yq -r ".vars.$1 // \"\"" "$ROOT_TASKFILE" | sed -n 's/.*default "\([^"]*\)".*/\1/p'
}

registry=$(default_of GLOBAL_REGISTRY)
namespace=$(default_of IMAGE_NAMESPACE)
[[ -n "$registry" ]] || bad "$ROOT_TASKFILE: no default for GLOBAL_REGISTRY"
[[ -n "$namespace" ]] || bad "$ROOT_TASKFILE: no default for IMAGE_NAMESPACE"

workflow_registry=$(yq -r '.env.REGISTRY // ""' "$WORKFLOW")
[[ "$workflow_registry" == "$registry" ]] ||
    bad "$WORKFLOW publishes to '$workflow_registry', $ROOT_TASKFILE defaults to '$registry'"

workflow_namespace=$(yq -r '.env.IMAGE_NAMESPACE // ""' "$WORKFLOW")
[[ "$workflow_namespace" == "$OWNER_EXPRESSION" ]] ||
    bad "$WORKFLOW: IMAGE_NAMESPACE is '$workflow_namespace', expected '$OWNER_EXPRESSION'"

owner=$(sed -n 's|.*repositoryUrl: *"https://github.com/\([^/]*\)/.*|\1|p' "$RELEASE_CONFIG")
[[ "$owner" == "$namespace" ]] ||
    bad "$ROOT_TASKFILE defaults IMAGE_NAMESPACE to '$namespace', the repository owner in $RELEASE_CONFIG is '$owner'"

# The directories the component tasks name, as taskfile-drift.test.sh reads
# them.
components=$(yq -r '.tasks | to_entries
    | map(select(((.value.cmds // [])[0].task // "") == strenv(COMPONENT_TASK)))
    | .[].value.cmds[0].vars[strenv(COMPONENT_DIR_VAR)]' "$ROOT_TASKFILE" | sort)
matrix=$(yq -r '.jobs.publish.strategy.matrix.component // [] | .[]' "$WORKFLOW" | sort)
[[ -n "$components" ]] || bad "$ROOT_TASKFILE reaches no components — this check is asserting nothing"
[[ "$components" == "$matrix" ]] ||
    bad "$WORKFLOW publishes [$(echo $matrix)], $ROOT_TASKFILE reaches [$(echo $components)]"

# Rendered rather than read from values.yaml: the image line the chart
# produces is what a cluster pulls, tag included. Packaged first because only
# `helm package` sets appVersion, and the tag defaults to it.
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT

for component in $components; do
    name=$(yq -r '.vars.PROJECT_NAME // ""' "$component/Taskfile.yaml")
    want="$registry/$namespace/$name:$PROBE_VERSION"
    helm package "$component/$CHART_DIR" --version "$PROBE_VERSION" \
        --app-version "$PROBE_VERSION" --destination "$scratch" >/dev/null
    required=()
    [[ -f "$component/$CHART_DIR/$REQUIRED_VALUES" ]] && required=(-f "$component/$CHART_DIR/$REQUIRED_VALUES")
    got=$(helm template probe "$scratch/$name-$PROBE_VERSION.tgz" ${required[@]+"${required[@]}"} |
        sed -n 's/^ *image: *"\{0,1\}\([^"]*\)"\{0,1\}$/\1/p' | sort -u)
    [[ "$got" == "$want" ]] ||
        bad "$component: the chart pulls '$(echo $got)', release.yaml publishes '$want'"
done

# The library's chart repository, from the copy each component keeps in its
# committed lock: the components include the release module, the entry points
# do not.
wf_chart_path=$(yq -r '.env.CHART_PATH' "$WORKFLOW")
for component in $components; do
    release_lib=$(find "$component/$REMOTE_LOCK" -maxdepth 1 -name 'git.github.com.release.*.yaml' 2>/dev/null | head -1)
    if [[ -z "$release_lib" ]]; then
        bad "$component: the release task library is not under $component/$REMOTE_LOCK"
        continue
    fi
    lib_chart_path=$(yq -r '.vars._REL_CHART_REPO' "$release_lib" | sed 's|.*}}/||')
    [[ "$lib_chart_path" == "$wf_chart_path" ]] ||
        bad "$component: release.yaml signs charts under '$wf_chart_path', the task library pushes them under '$lib_chart_path'"
done

[[ "$fail" == 0 ]] || exit 1

echo "publish target: $(echo $components | wc -w | tr -d ' ') components → $registry/$namespace"
