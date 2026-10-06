#!/usr/bin/env bash
# chart-alerts.test.sh — the backend chart's PrometheusRule, checked on the
# rendered manifest.
#
# A traffic alert over a series Paladin does not export loads cleanly and
# never fires: the rule reads as written, the dashboard of firing alerts stays
# empty, and that looks the same as a healthy api. So the rendered rules are
# parsed by promtool, driven with otelconnect's series by the unit tests in
# chart-alerts.promtool.yaml, and their expressions pinned here.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no cluster.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

readonly CHART=backend/deploy/chart
readonly RELEASE=paladin-core
readonly NAMESPACE=paladin
readonly REQUIRED_VALUES=ci/required-values.yaml
readonly RULE_TESTS=scripts/chart-alerts.promtool.yaml
# The file name RULE_TESTS lists under rule_files.
readonly RENDERED_RULES=prometheusrule.yaml
readonly RUNBOOKS=docs/runbooks
readonly SELECTOR='namespace="paladin"'

for tool in helm yq promtool; do
    command -v "$tool" >/dev/null 2>&1 || {
        echo "!!! $tool is not installed; the chart alerts went unchecked" >&2
        exit 1
    }
done

scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT

fail=0
cases=0
bad() { echo "!!! $*" >&2; fail=1; }

# render <name> [helm args...] — the PrometheusRule's spec, as a Prometheus
# rule file, into $scratch/<name>.yaml.
render() {
    local name=$1
    shift
    helm template "$RELEASE" "$CHART" --namespace "$NAMESPACE" -f "$CHART/$REQUIRED_VALUES" \
        --set metrics.alerts.enabled=true "$@" --show-only templates/prometheusrule.yaml |
        yq '.spec' >"$scratch/$name.yaml"
}

# rule <name> <alert> <field> — one field of one rendered alert.
rule() {
    yq -r '.groups[].rules[] | select(.alert == "'"$2"'") | '"$3" "$scratch/$1.yaml"
}

check() {
    cases=$((cases + 1))
    local what=$1 got=$2 want=$3
    [[ "$got" == "$want" ]] || bad "$what:"$'\n'"got:"$'\n'"$got"$'\n'"want:"$'\n'"$want"
}

# ─── expressions ─────────────────────────────────────────────────────────────

render defaults
check "defaults: PaladinApiCrashLooping expr" "$(rule defaults PaladinApiCrashLooping .expr)" "$(cat <<'PROMQL'
increase(kube_pod_container_status_restarts_total{namespace="paladin",container="paladin-core"}[15m]) > 2
PROMQL
)"
check "defaults: PaladinApiNotReady expr" "$(rule defaults PaladinApiNotReady .expr)" "$(cat <<'PROMQL'
kube_pod_status_ready{namespace="paladin",condition="true"} == 0
and on (pod, namespace) kube_pod_labels{label_app_kubernetes_io_component="api"}
PROMQL
)"
check "defaults: PaladinApiHighErrorRate expr" "$(rule defaults PaladinApiHighErrorRate .expr)" "$(cat <<'PROMQL'
(
  sum(rate(rpc_server_call_duration_seconds_count{error_type=~"UNKNOWN|DEADLINE_EXCEEDED|UNIMPLEMENTED|INTERNAL|UNAVAILABLE|DATA_LOSS"}[5m]))
  /
  sum(rate(rpc_server_call_duration_seconds_count[5m]))
) > 0.05
PROMQL
)"
check "defaults: PaladinApiHighLatency expr" "$(rule defaults PaladinApiHighLatency .expr)" "$(cat <<'PROMQL'
histogram_quantile(
  0.99,
  sum by (le) (rate(rpc_server_call_duration_seconds_bucket[5m]))
) > 1.5
PROMQL
)"

render selector -f "$CHART/values-prod.yaml" --set-string "metrics.alerts.rpcSelector=$SELECTOR" \
    --set metrics.alerts.rules.crashLooping.restarts=5 --set metrics.alerts.rules.crashLooping.window=30m
check "selector: PaladinApiCrashLooping expr" "$(rule selector PaladinApiCrashLooping .expr)" "$(cat <<'PROMQL'
increase(kube_pod_container_status_restarts_total{namespace="paladin",container="paladin-core"}[30m]) > 5
PROMQL
)"
check "selector: PaladinApiCrashLooping description" "$(rule selector PaladinApiCrashLooping .annotations.description | sed -n 2p)" \
    "has restarted more than 5 times in 30m. Likely panic on startup,"
check "selector: PaladinApiHighErrorRate expr" "$(rule selector PaladinApiHighErrorRate .expr)" "$(cat <<'PROMQL'
(
  sum(rate(rpc_server_call_duration_seconds_count{namespace="paladin",error_type=~"UNKNOWN|DEADLINE_EXCEEDED|UNIMPLEMENTED|INTERNAL|UNAVAILABLE|DATA_LOSS"}[5m]))
  /
  sum(rate(rpc_server_call_duration_seconds_count{namespace="paladin"}[5m]))
) > 0.01
PROMQL
)"
check "selector: PaladinApiHighLatency expr" "$(rule selector PaladinApiHighLatency .expr)" "$(cat <<'PROMQL'
histogram_quantile(
  0.99,
  sum by (le) (rate(rpc_server_call_duration_seconds_bucket{namespace="paladin"}[5m]))
) > 0.8
PROMQL
)"

# The /metrics handler's own series — what these rules read before.
cases=$((cases + 1))
if grep -q promhttp_metric_handler "$scratch/defaults.yaml"; then
    bad "defaults: a rule reads promhttp_metric_handler_*, the scrape endpoint's own series"
fi

# ─── runbooks ────────────────────────────────────────────────────────────────

base=$(yq -r '.metrics.alerts.runbookBaseUrl' "$CHART/values.yaml")
for alert in PaladinApiHighErrorRate PaladinApiHighLatency; do
    cases=$((cases + 1))
    url=$(rule defaults "$alert" '.annotations.runbook_url')
    file=${url#"$base/"}
    if [[ "$file" == "$url" ]]; then
        bad "$alert: runbook_url '$url' is not under $base"
    elif [[ ! -f "$RUNBOOKS/$file" ]]; then
        bad "$alert: runbook_url names $RUNBOOKS/$file, which does not exist"
    fi
done

# ─── promtool ────────────────────────────────────────────────────────────────

for name in defaults selector; do
    cases=$((cases + 1))
    promtool check rules "$scratch/$name.yaml" >/dev/null || bad "$name: promtool rejects the rendered rules"
done

mkdir "$scratch/unit"
cp "$scratch/defaults.yaml" "$scratch/unit/$RENDERED_RULES"
cp "$RULE_TESTS" "$scratch/unit/"
cases=$((cases + 1))
(cd "$scratch/unit" && promtool test rules "$(basename "$RULE_TESTS")") || bad "promtool unit tests failed"

if [[ $fail -ne 0 ]]; then
    exit 1
fi
echo "chart alerts: $cases checks passed"
