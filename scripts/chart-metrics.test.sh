#!/usr/bin/env bash
# chart-metrics.test.sh — metrics.mode, checked on the rendered chart.
#
# ADR-0023: one switch decides how metrics leave the process, and the chart
# derives everything else from it — the exporter the application runs, the
# address it listens on, the container port, the PodMonitor. Each piece used
# to be its own value, and the shipped overlays combined them into a scrape of
# a TLS port at a path nothing served. So the pieces are checked against each
# other here, per mode, and the combinations that cannot work must be refused.
# The NetworkPolicy half is chart-netpol.py's.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no cluster.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

readonly CHART=backend/deploy/chart
readonly RELEASE=paladin-core
readonly NAMESPACE=paladin
readonly REQUIRED_VALUES=ci/required-values.yaml
readonly PORT_NAME=metrics
readonly METRICS_PATH=/metrics
readonly DEFAULT_PORT=9095
# Not the default, so a port that is hardcoded instead of read shows up.
readonly CUSTOM_PORT=19095
# The roles that serve /metrics, and the one that does not.
readonly METRICS_ROLES=(api admin worker dispatcher ingest)
readonly UNSCRAPED_ROLE=mcp
readonly ALL_ROLES=(--set deployments.ingest.enabled=true --set deployments.mcp.enabled=true)
readonly OTEL_ON=(--set config.otel.enabled=true)

for tool in helm yq; do
    command -v "$tool" >/dev/null 2>&1 || {
        echo "!!! $tool is not installed; the chart's metrics wiring went unchecked" >&2
        exit 1
    }
done

scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT

fail=0
cases=0
bad() { echo "!!! $*" >&2; fail=1; }

# render <name> [helm args...] — renders into $scratch/<name>.yaml.
render() {
    local name=$1
    shift
    helm template "$RELEASE" "$CHART" --namespace "$NAMESPACE" -f "$CHART/$REQUIRED_VALUES" "$@" >"$scratch/$name.yaml"
}

# refused <what> <message> [helm args...] — the render must fail, saying so.
refused() {
    local what=$1 message=$2
    shift 2
    cases=$((cases + 1))
    local err
    if err=$(helm template "$RELEASE" "$CHART" --namespace "$NAMESPACE" -f "$CHART/$REQUIRED_VALUES" "$@" 2>&1 >/dev/null); then
        bad "$what: rendered; want it refused"
    elif [[ "$err" != *"$message"* ]]; then
        bad "$what: refused for another reason: $err"
    fi
}

# otel <name> <key> — a key of the rendered application config's otel block.
otel() {
    yq -r 'select(.kind == "ConfigMap" and .metadata.name == "'"$RELEASE"'-config") | .data["config.yaml"]' \
        "$scratch/$1.yaml" | yq -r ".otel.$2"
}

# metrics_port <name> <role> — the role's metrics containerPort, or empty.
metrics_port() {
    yq -r 'select(.kind == "Deployment" and .metadata.name == "'"$RELEASE-$2"'")
        | .spec.template.spec.containers[0].ports[] | select(.name == "'"$PORT_NAME"'") | .containerPort' \
        "$scratch/$1.yaml"
}

# pod_monitors <name> — how many PodMonitors the render carries.
pod_monitors() {
    yq -r 'select(.kind == "PodMonitor") | .metadata.name' "$scratch/$1.yaml" | grep -c . || true
}

check() {
    cases=$((cases + 1))
    local what=$1 got=$2 want=$3
    [[ "$got" == "$want" ]] || bad "$what: got '$got', want '$want'"
}

# ─── push: the default ───────────────────────────────────────────────────────

render push "${ALL_ROLES[@]}"
check "push: exporter" "$(otel push metrics_exporter)" "otlp"
check "push: no PodMonitor" "$(pod_monitors push)" "0"
for role in "${METRICS_ROLES[@]}"; do
    check "push: $role declares no metrics port" "$(metrics_port push "$role")" ""
done

# ─── scrape ──────────────────────────────────────────────────────────────────

render scrape "${ALL_ROLES[@]}" "${OTEL_ON[@]}" --set metrics.mode=scrape
check "scrape: exporter" "$(otel scrape metrics_exporter)" "prometheus"
check "scrape: address" "$(otel scrape metrics_addr)" "0.0.0.0:$DEFAULT_PORT"
check "scrape: one PodMonitor" "$(pod_monitors scrape)" "1"
check "scrape: PodMonitor port" \
    "$(yq -r 'select(.kind == "PodMonitor") | .spec.podMetricsEndpoints[0].port' "$scratch/scrape.yaml")" "$PORT_NAME"
check "scrape: PodMonitor path" \
    "$(yq -r 'select(.kind == "PodMonitor") | .spec.podMetricsEndpoints[0].path' "$scratch/scrape.yaml")" "$METRICS_PATH"
check "scrape: PodMonitor selects the release" \
    "$(yq -r 'select(.kind == "PodMonitor") | .spec.selector.matchLabels["app.kubernetes.io/instance"]' "$scratch/scrape.yaml")" "$RELEASE"
for role in "${METRICS_ROLES[@]}"; do
    check "scrape: $role metrics port" "$(metrics_port scrape "$role")" "$DEFAULT_PORT"
done
check "scrape: $UNSCRAPED_ROLE declares no metrics port" "$(metrics_port scrape "$UNSCRAPED_ROLE")" ""

render custom "${ALL_ROLES[@]}" "${OTEL_ON[@]}" --set metrics.mode=scrape --set metrics.port=$CUSTOM_PORT
check "custom port: address" "$(otel custom metrics_addr)" "0.0.0.0:$CUSTOM_PORT"
for role in "${METRICS_ROLES[@]}"; do
    check "custom port: $role metrics port" "$(metrics_port custom "$role")" "$CUSTOM_PORT"
done

# ─── off ─────────────────────────────────────────────────────────────────────

render off "${OTEL_ON[@]}" --set metrics.mode=off
check "off: exporter" "$(otel off metrics_exporter)" "none"
check "off: no PodMonitor" "$(pod_monitors off)" "0"

# ─── the overlays that run Prometheus Operator scrape ────────────────────────

for env in dev staging prod; do
    render "$env" -f "$CHART/values-$env.yaml"
    check "$env: exporter" "$(otel "$env" metrics_exporter)" "prometheus"
    check "$env: one PodMonitor" "$(pod_monitors "$env")" "1"
done

# ─── refused ─────────────────────────────────────────────────────────────────

refused "scrape without otel" "metrics.mode: scrape needs config.otel.enabled" --set metrics.mode=scrape
refused "exporter under config" "rendered from metrics.mode and metrics.port" \
    --set config.otel.metrics_exporter=prometheus
refused "address under config" "rendered from metrics.mode and metrics.port" \
    --set-string config.otel.metrics_addr=0.0.0.0:$CUSTOM_PORT
refused "unknown mode" "/metrics/mode" --set metrics.mode=pull
refused "the removed ServiceMonitor" "serviceMonitor" --set metrics.serviceMonitor.enabled=true

if [[ $fail -ne 0 ]]; then
    exit 1
fi
echo "chart metrics: $cases checks passed"
