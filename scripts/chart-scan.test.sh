#!/usr/bin/env bash
# chart-scan.test.sh — policy-scan the manifests the charts actually render,
# once per values file we ship, with checkov and trivy.
#
# Both tools can scan a chart directory themselves, and both do it badly here:
# they render it with the chart's own defaults only, so no overlay is ever
# read, and the backend chart refuses to render without a database and an
# object store — which each tool logs as a warning before skipping the chart
# and reporting clean. So the charts are rendered here, the way chart-values
# renders them, and the scanners read the result.
#
# A scan that reads nothing passes, which is how the backend chart went
# unscanned. Two assertions close that: every rendered file must show up in
# each tool's results, and a planted manifest with known violations must be
# reported — so an ignore file or a skip list that swallows everything turns
# this red rather than quiet.
#
# CHECKOV_VERSION comes from Taskfile.dev.yaml, the same pin `checkov:scan`
# runs.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
charts=("$root/backend/deploy/chart" "$root/frontend/deploy/chart")

readonly REQUIRED_VALUES=ci/required-values.yaml
readonly CHECKOV_CONFIG="$root/.checkov.yaml"
readonly TRIVY_IGNORE="$root/.trivyignore.yaml"
# Rendered into every namespaced resource. Without one Helm writes `default`,
# which both tools flag as a finding about the renderer rather than the chart.
readonly NAMESPACE=paladin
readonly CANARY=canary.yaml
: "${CHECKOV_VERSION:?set CHECKOV_VERSION to the pin in Taskfile.dev.yaml}"

for tool in helm trivy jq yq; do
    command -v "$tool" >/dev/null 2>&1 || { echo "!!! $tool is not installed; the rendered charts went unscanned" >&2; exit 1; }
done

if [[ "$(checkov --version 2>/dev/null || true)" == "$CHECKOV_VERSION" ]]; then
    run_checkov() { checkov "$@"; }
elif command -v pipx >/dev/null 2>&1; then
    run_checkov() { pipx run "checkov==$CHECKOV_VERSION" "$@"; }
else
    echo "!!! checkov $CHECKOV_VERSION is pinned and neither it nor pipx is installed" >&2
    exit 1
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
out="$tmp/rendered"

# ── render ──────────────────────────────────────────────────────────────────
for chart in "${charts[@]}"; do
    name=$(basename "$(dirname "$(dirname "$chart")")")
    mkdir -p "$out/$name"
    # Named after the chart, as docs/install.md installs it.
    release=$(yq '.name' "$chart/Chart.yaml")
    required=()
    [[ -f "$chart/$REQUIRED_VALUES" ]] && required=(-f "$chart/$REQUIRED_VALUES")
    for values in "$chart"/values*.yaml; do
        # The same order as chart-values.test.sh: after the chart's own
        # values.yaml, before an overlay.
        if [[ "$(basename "$values")" == values.yaml ]]; then
            args=(-f "$values" ${required[@]+"${required[@]}"})
        else
            args=(${required[@]+"${required[@]}"} -f "$values")
        fi
        helm template "$release" "$chart" -n "$NAMESPACE" "${args[@]}" >"$out/$name/$(basename "$values")"
    done
done

# Every rendered file, relative to $out, as both tools will name it.
rendered=$(cd "$out" && find . -name '*.yaml' | sed 's|^\./||' | sort)
[[ -n "$rendered" ]] || { echo "!!! nothing rendered — this gate is asserting nothing" >&2; exit 1; }

# A privileged pod bound to a wildcard ClusterRole: findings for both tools
# under rules no ignore list here accepts.
cat >"$out/$CANARY" <<'EOF'
apiVersion: v1
kind: Pod
metadata:
  name: canary
  namespace: paladin
spec:
  containers:
    - name: canary
      image: canary:1.0.0
      securityContext:
        privileged: true
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: canary
rules:
  - apiGroups: ["*"]
    resources: ["*"]
    verbs: ["*"]
EOF

fail=0
# covered <tool> <files the tool reported on>: every rendered file is among them.
covered() {
    local missing
    missing=$(comm -23 <(printf '%s\n' "$rendered") <(printf '%s\n' "$2" | sort -u))
    if [[ -n "$missing" ]]; then
        echo "!!! $1 did not read:" >&2
        printf '      %s\n' $missing >&2
        fail=1
    fi
}

# ── checkov ─────────────────────────────────────────────────────────────────
# Exits non-zero on the canary by design; the JSON decides.
run_checkov --config-file "$CHECKOV_CONFIG" -d "$out" --framework kubernetes \
    -o json >"$tmp/checkov.json" 2>"$tmp/checkov.err" || true
if ! jq -e 'type == "object" and .check_type == "kubernetes"' "$tmp/checkov.json" >/dev/null 2>&1; then
    echo "!!! checkov produced no kubernetes report" >&2
    sed 's/^/      /' "$tmp/checkov.err" >&2
    exit 1
fi
checkov_path='.file_path | ltrimstr("/")'
covered checkov "$(jq -r ".results.passed_checks[], .results.failed_checks[] | $checkov_path" "$tmp/checkov.json")"
if ! jq -e --arg c "$CANARY" "[.results.failed_checks[] | select(($checkov_path) == \$c)] | length > 0" "$tmp/checkov.json" >/dev/null; then
    echo "!!! checkov reported nothing on the planted violations — the config is swallowing findings" >&2
    fail=1
fi
findings=$(jq -r --arg c "$CANARY" \
    ".results.failed_checks[] | select(($checkov_path) != \$c) | \"\(.file_path)  \(.check_id)  \(.resource)  \(.check_name)\"" \
    "$tmp/checkov.json")
if [[ -n "$findings" ]]; then
    echo "!!! checkov:" >&2
    printf '%s\n' "$findings" | sed 's/^/      /' >&2
    fail=1
fi

# ── trivy ───────────────────────────────────────────────────────────────────
trivy config --quiet --exit-code 0 --ignorefile "$TRIVY_IGNORE" -f json -o "$tmp/trivy.json" "$out"
covered trivy "$(jq -r '.Results[] | select(.MisconfSummary.Successes > 0) | .Target' "$tmp/trivy.json")"
if ! jq -e --arg c "$CANARY" '[.Results[] | select(.Target == $c) | .Misconfigurations[]?] | length > 0' "$tmp/trivy.json" >/dev/null; then
    echo "!!! trivy reported nothing on the planted violations — the ignore file is swallowing findings" >&2
    fail=1
fi
findings=$(jq -r --arg c "$CANARY" \
    '.Results[] | select(.Target != $c) | .Target as $t | .Misconfigurations[]? | "\($t)  \(.ID)  \(.Severity)  \(.Message)"' \
    "$tmp/trivy.json")
if [[ -n "$findings" ]]; then
    echo "!!! trivy:" >&2
    printf '%s\n' "$findings" | sed 's/^/      /' >&2
    fail=1
fi

[[ "$fail" == 0 ]] || exit 1
echo "rendered charts: $(printf '%s\n' "$rendered" | wc -l | tr -d ' ') files scanned by checkov and trivy, no findings"
