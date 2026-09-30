#!/usr/bin/env bash
# chart-defaults.test.sh — what the charts wire by themselves, asserted on
# the rendered manifests.
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
readonly REQUIRED_VALUES=ci/required-values.yaml

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
    helm template "$RELEASE" "$CHART" --namespace "$NAMESPACE" -f "$CHART/$REQUIRED_VALUES" "$@" >"$scratch/$name.yaml"
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

# ─── PostgreSQL ───────────────────────────────────────────────────────────────

check "postgres: dsn built from the block" "$(config defaults .datastores.postgres.dsn)" \
    "postgres://paladin_app@postgres.example.internal:5432/paladin?sslmode=require"
check "postgres: migrate dsn built from the block" "$(config defaults .datastores.postgres.migrate_dsn)" \
    "postgres://paladin_migrate@postgres.example.internal:5432/paladin?sslmode=require"
check "postgres: app password ref" "$(config defaults .datastores.postgres.password_secret.name)" "paladin-postgres-app"
check "postgres: refs default to the release namespace" \
    "$(config defaults '.datastores.postgres.password_secret.namespace // "release"')" "release"
for secret in paladin-postgres-app paladin-postgres-migrate; do
    cases=$((cases + 1))
    rbac_names defaults | grep -x "$secret" >/dev/null || bad "postgres: RBAC cannot read $secret"
done
check "postgres: a namespaced Role by default" \
    "$(yq -r 'select(.kind == "Role" or .kind == "ClusterRole") | .kind' "$scratch/defaults.yaml")" "Role"

render other-ns --set postgres.app.namespace=database
check "postgres: namespace override lands on the ref" \
    "$(config other-ns .datastores.postgres.password_secret.namespace)" "database"

# refuse <name> <expected message> [helm args...] — the render must fail and
# say why. Each case passes the required values and unsets what it tests.
refuse() {
    local name=$1 want=$2
    shift 2
    cases=$((cases + 1))
    if helm template "$RELEASE" "$CHART" --namespace "$NAMESPACE" "$@" >/dev/null 2>"$scratch/$name.err"; then
        bad "$name: rendered, want a refusal mentioning '$want'"
    elif ! grep -q -- "$want" "$scratch/$name.err"; then
        bad "$name: refused without mentioning '$want': $(head -1 "$scratch/$name.err")"
    fi
}
refuse no-database "no database configured" -f "$CHART/$REQUIRED_VALUES" --set postgres.host=
refuse no-app-secret "postgres.app.existingSecret is required" \
    -f "$CHART/$REQUIRED_VALUES" --set postgres.app.existingSecret=

# ─── object store ────────────────────────────────────────────────────────────

refuse no-endpoint "config.storage.backends.primary.endpoint is required" \
    -f "$CHART/$REQUIRED_VALUES" --set config.storage.backends.primary.endpoint=
refuse no-s3-keys "storage.s3CredentialsSecret.accessKey is required" \
    -f "$CHART/$REQUIRED_VALUES" --set storage.s3CredentialsSecret.accessKey=
render s3-existing --set storage.s3CredentialsSecret.accessKey= --set storage.s3CredentialsSecret.secretKey= \
    --set storage.s3CredentialsSecret.existingSecret=my-s3
check "s3: existingSecret needs no keys" "$(config s3-existing .storage.backends.primary.auth.access_key_secret.name)" "my-s3"

cases=$((cases + 1))
helm template "$RELEASE" "$CHART" --namespace "$NAMESPACE" -f "$CHART/$REQUIRED_VALUES" \
    --set postgres.host= --set config.datastores.postgres.dsn=postgres://u@h/d >"$scratch/raw-dsn.yaml" ||
    bad "postgres: a DSN written under config.datastores.postgres was refused"
check "postgres: a written DSN is used as is" "$(config raw-dsn .datastores.postgres.dsn)" "postgres://u@h/d"

# ─── internal TLS ────────────────────────────────────────────────────────────

# deployment <render> <role> <yq path> — a value from one role's Deployment.
deployment() {
    yq ea -r '[select(.kind == "Deployment" and .metadata.name == "'"$RELEASE-$2"'")] | .[0] | '"$3" "$scratch/$1.yaml"
}

check "tls off: data listener plain" "$(config defaults .api.server.data.tls.enabled)" "false"
check "tls off: bridge dials http" "$(config defaults .mcp.upstreams.admin_url)" "http://$RELEASE-admin:8090"
check "tls off: api probe plain" "$(deployment defaults api '.spec.template.spec.containers[0].livenessProbe.httpGet.scheme // "HTTP"')" "HTTP"
check "tls off: no certificate mounted" \
    "$(deployment defaults api '[.spec.template.spec.volumes[] | select(.name == "internal-tls")] | length')" "0"

render tls --set internalTLS.enabled=true --set internalTLS.existingSecret=planes-tls
for listener in .api.server.data .api.server.iam .admin.server; do
    check "tls on: $listener enabled" "$(config tls "$listener.tls.enabled")" "true"
    check "tls on: $listener certificate" "$(config tls "$listener.tls.cert_path")" "/etc/paladin-mtls/tls.crt"
done
check "tls on: bridge dials https" "$(config tls .mcp.upstreams.admin_url)" "https://$RELEASE-admin:8090"
check "tls on: bridge trusts the CA" "$(config tls .mcp.upstreams.tls.ca_path)" "/etc/paladin-mtls/ca.crt"
for role in api admin mcp; do
    check "tls on: $role mounts the certificate" \
        "$(deployment tls "$role" '.spec.template.spec.volumes[] | select(.name == "internal-tls") | .secret.secretName')" "planes-tls"
done
check "tls on: worker does not mount it" \
    "$(deployment tls worker '[.spec.template.spec.volumes[] | select(.name == "internal-tls")] | length')" "0"
check "tls on: api probe over HTTPS" "$(deployment tls api '.spec.template.spec.containers[0].readinessProbe.httpGet.scheme')" "HTTPS"
check "tls on: mcp probe stays plain" "$(deployment tls mcp '.spec.template.spec.containers[0].livenessProbe.httpGet.scheme // "HTTP"')" "HTTP"

refuse tls-no-secret "internalTLS.existingSecret is required" -f "$CHART/$REQUIRED_VALUES" --set internalTLS.enabled=true

# ─── console: where the backend is ────────────────────────────────────────────

readonly CONSOLE_CHART=frontend/deploy/chart

# console_env <render> <var> — an env var of the console container.
console_env() {
    yq ea -r '[select(.kind == "Deployment")] | .[0] | .spec.template.spec.containers[0].env[] | select(.name == "'"$2"'") | .value' \
        "$scratch/console-$1.yaml"
}
console() {
    local name=$1
    shift
    helm template paladin-console "$CONSOLE_CHART" --namespace "$NAMESPACE" "$@" >"$scratch/console-$name.yaml"
}

console defaults
check "console: data URL from the default release" "$(console_env defaults PALADIN_DATA_URL)" \
    "http://paladin-core-api.$NAMESPACE.svc.cluster.local:8080"
check "console: health roles" "$(console_env defaults PALADIN_HEALTH_ROLES)" "api,admin,worker,mcp,dispatcher"

console other-release --set backend.release=pic --set backend.namespace=platform
check "console: release without the chart name is suffixed" "$(console_env other-release PALADIN_ADMIN_URL)" \
    "http://pic-paladin-core-admin.platform.svc.cluster.local:8090"

console explicit --set backend.urls.iam=https://iam.example
check "console: an explicit URL wins" "$(console_env explicit PALADIN_IAM_URL)" "https://iam.example"

console tls --set backend.tls=true --set backend.caSecret.name=planes-ca
check "console: tls dials data over https" "$(console_env tls PALADIN_DATA_URL)" \
    "https://paladin-core-api.$NAMESPACE.svc.cluster.local:8080"
check "console: tls leaves the ops ports plain" "$(console_env tls PALADIN_WORKER_URL)" \
    "http://paladin-core-worker.$NAMESPACE.svc.cluster.local:8099"
check "console: tls trusts the mounted CA" "$(console_env tls NODE_EXTRA_CA_CERTS)" "/etc/paladin-backend-ca/ca.crt"
check "console: tls mounts the CA Secret" \
    "$(yq ea -r '[select(.kind == "Deployment")] | .[0] | .spec.template.spec.volumes[] | select(.name == "backend-ca") | .secret.secretName' "$scratch/console-tls.yaml")" \
    "planes-ca"
cases=$((cases + 1))
if helm template paladin-console "$CONSOLE_CHART" --set backend.tls=true >/dev/null 2>"$scratch/console-no-ca.err" ||
    ! grep -q "backend.caSecret.name is required" "$scratch/console-no-ca.err"; then
    bad "console: backend.tls without a caSecret was not refused"
fi

# ─── ingress ─────────────────────────────────────────────────────────────────

# kind_count <file> <kind> — how many objects of that kind a render holds.
kind_count() { yq ea '[select(.kind == "'"$2"'")] | length' "$1"; }

check "ingress: off by default" "$(kind_count "$scratch/defaults.yaml" Ingress)" "0"
check "console ingress: off by default" "$(kind_count "$scratch/console-defaults.yaml" Ingress)" "0"

render ingress --set ingress.enabled=true --set 'ingress.hosts={paladin.example.com}'
ingress() { yq ea -r '[select(.kind == "Ingress")] | .[0] | '"$2" "$scratch/$1.yaml"; }
check "ingress: Caddy class" "$(ingress ingress .spec.ingressClassName)" "caddy"
check "ingress: iam path first" "$(ingress ingress '.spec.rules[0].http.paths[0].path')" "/paladin.iam.v1.*"
check "ingress: iam to the iam port" "$(ingress ingress '.spec.rules[0].http.paths[0].backend.service.port.number')" "8085"
check "ingress: the rest to the data port" "$(ingress ingress '.spec.rules[0].http.paths[1].backend.service.port.number')" "8080"
check "ingress: plain backends need no protocol annotation" \
    "$(ingress ingress '.metadata.annotations["caddy.ingress.kubernetes.io/backend-protocol"] // "none"')" "none"

render ingress-tls --set ingress.enabled=true --set 'ingress.hosts={paladin.example.com}' \
    --set internalTLS.enabled=true --set internalTLS.existingSecret=planes-tls
check "ingress: Caddy dials TLS planes over https" \
    "$(ingress ingress-tls '.metadata.annotations["caddy.ingress.kubernetes.io/backend-protocol"]')" "https"

refuse ingress-no-host "ingress.hosts must name at least one host" \
    -f "$CHART/$REQUIRED_VALUES" --set ingress.enabled=true

console console-ingress --set ingress.enabled=true --set 'ingress.hosts={console.example.com}'
check "console ingress: to the console Service" \
    "$(yq ea -r '[select(.kind == "Ingress")] | .[0] | .spec.rules[0].http.paths[0].backend.service.port.number' "$scratch/console-console-ingress.yaml")" "3000"

[[ "$fail" == 0 ]] || exit 1
echo "chart defaults: $cases assertions hold"
