#!/usr/bin/env bash
# chart-schema.test.sh — every chart's values.schema.json must accept the
# values files we ship and reject a key that does not exist.
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
# Runs from `task verify-all` — seconds, no cluster.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
charts=("$root/backend/deploy/chart" "$root/frontend/deploy/chart")

if ! command -v helm >/dev/null 2>&1; then
    echo "!!! helm is not installed; the chart value schemas went unchecked" >&2
    exit 1
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

for chart in "${charts[@]}"; do
    name=$(basename "$(dirname "$(dirname "$chart")")")

    if [[ ! -f "$chart/values.schema.json" ]]; then
        echo "!!! $name: no values.schema.json — every chart key is unvalidated" >&2
        exit 1
    fi

    # 1. The shipped values must all pass. A schema that rejects prod is worse
    #    than none: it fails at deploy time, on the environment that matters.
    for values in "$chart"/values*.yaml; do
        if ! helm template schematest "$chart" -f "$values" >/dev/null 2>"$tmp/err"; then
            {
                echo "!!! $name: $(basename "$values") does not satisfy values.schema.json"
                sed 's/^/      /' "$tmp/err"
            } >&2
            exit 1
        fi
    done

    # 2. An unknown top-level key must be refused. This is the assertion that
    #    makes the rest of the file mean something.
    printf 'aKeyThatDoesNotExist: true\n' >"$tmp/unknown.yaml"
    if helm template schematest "$chart" -f "$tmp/unknown.yaml" >/dev/null 2>&1; then
        echo "!!! $name: values.schema.json accepted an unknown top-level key" >&2
        echo "      additionalProperties is not set, or the schema is not being read" >&2
        exit 1
    fi

    echo "$name: $(ls "$chart"/values*.yaml | wc -l | tr -d ' ') values files accepted, unknown keys refused"
done
