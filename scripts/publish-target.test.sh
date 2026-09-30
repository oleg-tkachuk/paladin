#!/usr/bin/env bash
# publish-target.test.sh — where release.yaml publishes, where Taskfile.yaml
# points, and what the charts pull by default must be the same place.
#
# The image path is spelled in three files that cannot import from one
# another: the workflow's env, the root Taskfile's defaults, and the image
# each chart renders. A chart whose default names a
# path nothing publishes to installs cleanly and then sits in ImagePullBackOff.
#
# Also checks the workflow's component matrix against the components
# Taskfile.yaml includes, so a third component is not added to the entry points
# and forgotten by the release.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no Docker, no network.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

readonly ROOT_TASKFILE=Taskfile.yaml
readonly WORKFLOW=.github/workflows/release.yaml
readonly RELEASE_CONFIG=release.config.cjs
readonly CHART_DIR=deploy/chart
# Any valid SemVer; it only has to come back out of the render unchanged.
readonly PROBE_VERSION=0.0.0-probe
# The inputs a chart cannot default, added to every render.
readonly REQUIRED_VALUES=ci/required-values.yaml
# What the workflow's IMAGE_NAMESPACE must be: the repository owner, which is
# only known to Actions, so it is compared against the owner release.config.cjs
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

# The includes naming a path in this repository, as taskfile-drift.test.sh
# reads them; the library modules go through TASKLIB.
components=$(yq -r '.includes | to_entries
    | map(select((.value.taskfile // "") | test("TASKLIB") | not)) | .[].value.dir' "$ROOT_TASKFILE" | sort)
matrix=$(yq -r '.jobs.publish.strategy.matrix.component // [] | .[]' "$WORKFLOW" | sort)
[[ -n "$components" ]] || bad "$ROOT_TASKFILE includes no components — this check is asserting nothing"
[[ "$components" == "$matrix" ]] ||
    bad "$WORKFLOW publishes [$(echo $matrix)], $ROOT_TASKFILE includes [$(echo $components)]"

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

[[ "$fail" == 0 ]] || exit 1

echo "publish target: $(echo $components | wc -w | tr -d ' ') components → $registry/$namespace"
