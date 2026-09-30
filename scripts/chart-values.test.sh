#!/usr/bin/env bash
# chart-values.test.sh — the values files we ship must be accepted by their
# chart's schema, must be refused when a key does not exist, and must each
# render as a well-formed multi-document stream.
#
# A mistyped chart value is silent. `deployments.api.replica: 5` renders
# `replicas: 2` — the default — and `helm template` exits 0, so the operator
# believes they set five and the cluster runs two. Nothing in the pipeline
# disagrees, because from Helm's side nothing went wrong.
#
# The schema closes that, and this closes the schema: a schema nobody has
# watched reject anything is indistinguishable from one that accepts
# everything.
#
# Every check here runs once per values file, on purpose. The chart's own
# `chart:verify` renders the defaults and nothing else, which is how a broken
# prod-only template stayed green: backend renders 25 resources under
# values.yaml and 33 under values-prod.yaml, so six kinds — Certificate, HPA,
# IngressRoute, PrometheusRule, ServersTransport, ServiceMonitor — were
# reachable by no gate at all.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — seconds, no cluster.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
charts=("$root/backend/deploy/chart" "$root/frontend/deploy/chart")

if ! command -v helm >/dev/null 2>&1; then
    echo "!!! helm is not installed; the chart value schemas went unchecked" >&2
    exit 1
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

readonly REQUIRED_VALUES=ci/required-values.yaml

for chart in "${charts[@]}"; do
    name=$(basename "$(dirname "$(dirname "$chart")")")

    if [[ ! -f "$chart/values.schema.json" ]]; then
        echo "!!! $name: no values.schema.json — every chart key is unvalidated" >&2
        exit 1
    fi

    # The inputs a chart cannot default (ci/required-values.yaml), so a render
    # fails only for the reason being tested.
    required=()
    [[ -f "$chart/$REQUIRED_VALUES" ]] && required=(-f "$chart/$REQUIRED_VALUES")

    # 1. The shipped values must all pass. A schema that rejects prod is worse
    #    than none: it fails at deploy time, on the environment that matters.
    for values in "$chart"/values*.yaml; do
        # After the chart's own values.yaml, which would otherwise reset them;
        # before an overlay, which must win.
        if [[ "$(basename "$values")" == values.yaml ]]; then
            args=(-f "$values" ${required[@]+"${required[@]}"})
        else
            args=(${required[@]+"${required[@]}"} -f "$values")
        fi
        if ! helm template schematest "$chart" "${args[@]}" >"$tmp/render.yaml" 2>"$tmp/err"; then
            {
                echo "!!! $name: $(basename "$values") does not satisfy values.schema.json"
                sed 's/^/      /' "$tmp/err"
            } >&2
            exit 1
        fi

        # 1b. …and must render as separate documents. A `{{- ... -}}` that
        #     strips the newline before a `---` glues the separator onto the
        #     previous line (`automountServiceAccountToken: true---`); every
        #     YAML parser then reads the stream as one document and drops
        #     every resource after the first. The chart ships, the cluster
        #     runs a fraction of it, and helm reports nothing wrong.
        if grep -qE '[^[:space:]]---' "$tmp/render.yaml"; then
            {
                echo "!!! $name: $(basename "$values") renders a glued document separator"
                echo "      kubectl and ArgoCD drop every resource after the first."
                grep -nE '[^[:space:]]---' "$tmp/render.yaml" | sed 's/^/      /'
            } >&2
            exit 1
        fi
    done

    # 2. An unknown top-level key must be refused. This is the assertion that
    #    makes the rest of the file mean something.
    printf 'aKeyThatDoesNotExist: true\n' >"$tmp/unknown.yaml"
    if helm template schematest "$chart" ${required[@]+"${required[@]}"} -f "$tmp/unknown.yaml" >/dev/null 2>&1; then
        echo "!!! $name: values.schema.json accepted an unknown top-level key" >&2
        echo "      additionalProperties is not set, or the schema is not being read" >&2
        exit 1
    fi

    echo "$name: $(ls "$chart"/values*.yaml | wc -l | tr -d ' ') values files accepted, unknown keys refused, separators intact"
done
