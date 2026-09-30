#!/usr/bin/env bash
# chart-defaults.test.sh — what the backend chart wires by itself, asserted
# on the rendered manifests.
#
# A fresh `helm install` has to come up without the operator hand-writing
# Secrets the chart could have made, and an overlay that already answers a
# question has to render exactly as it did. Both halves are checked here, on
# the bytes Helm produces rather than on values.yaml.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no cluster.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

readonly CHART=backend/deploy/chart
readonly RELEASE=paladin-core
readonly NAMESPACE=paladin
readonly SIGNING_SECRET="$RELEASE-auth-signing-key"

for tool in helm yq; do
    command -v "$tool" >/dev/null 2>&1 || {
        echo "!!! $tool is not installed; the chart defaults went unchecked" >&2
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
    helm template "$RELEASE" "$CHART" --namespace "$NAMESPACE" "$@" >"$scratch/$name.yaml"
}

# config <name> <yq path> — a value from the rendered application config.
config() {
    yq -r 'select(.kind == "ConfigMap" and .metadata.name == "'"$RELEASE"'-config") | .data["config.yaml"]' \
        "$scratch/$1.yaml" | yq -r "$2"
}

# secret_exists <name> <secret> — whether the render carries that Secret.
secret_exists() {
    [[ -n "$(yq -r 'select(.kind == "Secret" and .metadata.name == "'"$2"'") | .metadata.name' "$scratch/$1.yaml")" ]]
}

# rbac_names <name> — the Secret names the resolver may read.
rbac_names() {
    yq -r 'select(.kind == "ClusterRole" or .kind == "Role") | .rules[0].resourceNames[]' "$scratch/$1.yaml"
}

check() {
    cases=$((cases + 1))
    local what=$1 got=$2 want=$3
    [[ "$got" == "$want" ]] || bad "$what: got '$got', want '$want'"
}

# ─── access-token signing key ────────────────────────────────────────────────

render defaults
check "defaults: app.env" "$(config defaults .app.env)" "prod"
check "defaults: issuer falls back to the release" "$(config defaults .auth.issuer)" "$RELEASE"
check "defaults: config reads the generated key" "$(config defaults .auth.signing_key_secret.name)" "$SIGNING_SECRET"
cases=$((cases + 1))
secret_exists defaults "$SIGNING_SECRET" || bad "defaults: no $SIGNING_SECRET Secret rendered"
cases=$((cases + 1))
rbac_names defaults | grep -x "$SIGNING_SECRET" >/dev/null || bad "defaults: RBAC cannot read $SIGNING_SECRET"
cases=$((cases + 1))
secret_exists defaults paladin-bootstrap-admin || bad "defaults: no bootstrap admin Secret rendered"
cases=$((cases + 1))
rbac_names defaults | grep -x paladin-bootstrap-admin >/dev/null || bad "defaults: RBAC cannot read the bootstrap admin Secret"

render inline --set config.auth.signing_key=inline-key --set config.auth.issuer=https://issuer.example
check "inline key: issuer kept" "$(config inline .auth.issuer)" "https://issuer.example"
check "inline key: no secret ref added" "$(config inline '.auth.signing_key_secret // "none"')" "none"
cases=$((cases + 1))
secret_exists inline "$SIGNING_SECRET" && bad "inline key: a signing key Secret was generated anyway"

render existing --set auth.signingKey.existingSecret=my-signing-key
check "existingSecret: config reads it" "$(config existing .auth.signing_key_secret.name)" "my-signing-key"
cases=$((cases + 1))
secret_exists existing "$SIGNING_SECRET" && bad "existingSecret: a signing key Secret was generated anyway"
cases=$((cases + 1))
rbac_names existing | grep -x my-signing-key >/dev/null || bad "existingSecret: RBAC cannot read my-signing-key"

render replaced-list --set 'rbac.secretReader.secretNames={only-this}'
cases=$((cases + 1))
rbac_names replaced-list | grep -x paladin-bootstrap-admin >/dev/null ||
    bad "a replaced rbac.secretReader.secretNames dropped the bootstrap admin Secret"

[[ "$fail" == 0 ]] || exit 1
echo "chart defaults: $cases assertions hold"
