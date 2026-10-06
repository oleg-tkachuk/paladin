#!/usr/bin/env bash
# chart-alerts.test.sh — the backend chart's PrometheusRule, checked on the
# rendered manifest. It carries every Paladin alert (ADR-0023).
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
# Every rule the chart ships, all enabled by default.
readonly RULE_COUNT=13
# The lifecycle rules have no runbook of their own: their descriptions say
# what to inspect. Every other rule links one.
readonly LIFECYCLE_RULES=3
readonly RUNBOOK_COUNT=$((RULE_COUNT - LIFECYCLE_RULES))

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
check "defaults: PaladinCrashLooping expr" "$(rule defaults PaladinCrashLooping .expr)" "$(cat <<'PROMQL'
label_replace(
  increase(kube_pod_container_status_restarts_total{namespace="paladin",container="paladin-core",pod=~"paladin-core-(admin|api|dispatcher|mcp|worker)-.+"}[15m]) > 2,
  "component", "$1", "pod", "paladin-core-(admin|api|dispatcher|mcp|worker)-.+"
)
PROMQL
)"
check "defaults: PaladinNotReady expr" "$(rule defaults PaladinNotReady .expr)" "$(cat <<'PROMQL'
label_replace(
  kube_pod_status_ready{namespace="paladin",condition="true",pod=~"paladin-core-(admin|api|dispatcher|mcp|worker)-.+"} == 0,
  "component", "$1", "pod", "paladin-core-(admin|api|dispatcher|mcp|worker)-.+"
)
PROMQL
)"
check "defaults: PaladinOOMKilled expr" "$(rule defaults PaladinOOMKilled .expr)" "$(cat <<'PROMQL'
label_replace(
  increase(kube_pod_container_status_restarts_total{namespace="paladin",container="paladin-core",pod=~"paladin-core-(admin|api|dispatcher|mcp|worker)-.+"}[15m]) > 0
  and on (namespace, pod, container)
  kube_pod_container_status_last_terminated_reason{namespace="paladin",container="paladin-core",reason="OOMKilled",pod=~"paladin-core-(admin|api|dispatcher|mcp|worker)-.+"} == 1,
  "component", "$1", "pod", "paladin-core-(admin|api|dispatcher|mcp|worker)-.+"
)
PROMQL
)"
check "defaults: PaladinOutboxNotDraining expr" "$(rule defaults PaladinOutboxNotDraining .expr)" "$(cat <<'PROMQL'
min_over_time(max(paladin_outbox_pending)[15m:1m]) > 500
PROMQL
)"
check "defaults: PaladinWorkerStalled expr" "$(rule defaults PaladinWorkerStalled .expr)" "$(cat <<'PROMQL'
(time() - paladin_worker_last_run_timestamp_seconds)
  > 5 * paladin_worker_interval_seconds
PROMQL
)"
check "defaults: rules rendered" "$(yq -r '[.groups[].rules[]] | length' "$scratch/defaults.yaml")" "$RULE_COUNT"
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
    --set metrics.alerts.rules.crashLooping.restarts=5 --set metrics.alerts.rules.crashLooping.window=30m \
    --set deployments.ingest.enabled=true --set metrics.alerts.rules.outboxNotDraining.rows=2000 \
    --set metrics.alerts.rules.workerStalled.intervals=3
check "selector: PaladinCrashLooping expr" "$(rule selector PaladinCrashLooping .expr)" "$(cat <<'PROMQL'
label_replace(
  increase(kube_pod_container_status_restarts_total{namespace="paladin",container="paladin-core",pod=~"paladin-core-(admin|api|dispatcher|ingest|mcp|worker)-.+"}[30m]) > 5,
  "component", "$1", "pod", "paladin-core-(admin|api|dispatcher|ingest|mcp|worker)-.+"
)
PROMQL
)"
check "selector: PaladinCrashLooping description" "$(rule selector PaladinCrashLooping .annotations.description | sed -n 2p)" \
    "has restarted more than 5 times in 30m. Likely panic on startup,"
check "selector: PaladinOutboxNotDraining summary" "$(rule selector PaladinOutboxNotDraining .annotations.summary)" \
    "Paladin outbox has not drained below 2000 rows in 15m"
check "selector: PaladinWorkerStalled expr" "$(rule selector PaladinWorkerStalled .expr)" "$(cat <<'PROMQL'
(time() - paladin_worker_last_run_timestamp_seconds)
  > 3 * paladin_worker_interval_seconds
PROMQL
)"
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

check "defaults: rules with a runbook" \
    "$(yq -r '[.groups[].rules[] | select(.annotations.runbook_url)] | length' "$scratch/defaults.yaml")" "$RUNBOOK_COUNT"
base=$(yq -r '.metrics.alerts.runbookBaseUrl' "$CHART/values.yaml")
for alert in $(yq -r '.groups[].rules[] | select(.annotations.runbook_url) | .alert' "$scratch/defaults.yaml"); do
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
