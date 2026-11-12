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
readonly PEPPER_SECRET="$RELEASE-api-token-hmac"
readonly SESSION_SECRET="$RELEASE-session-key"
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

# ─── release defaults ────────────────────────────────────────────────────────

render defaults
check "defaults: app.env" "$(config defaults .app.env)" "prod"
check "defaults: issuer falls back to the release" "$(config defaults .auth.issuer)" "$RELEASE"
cases=$((cases + 1))
secret_exists defaults paladin-bootstrap-admin || bad "defaults: no bootstrap admin Secret rendered"
cases=$((cases + 1))
rbac_names defaults | grep -x paladin-bootstrap-admin >/dev/null || bad "defaults: RBAC cannot read the bootstrap admin Secret"

render issuer --set config.auth.issuer=https://issuer.example
check "an issuer set is kept" "$(config issuer .auth.issuer)" "https://issuer.example"

# The shared HS256 key is gone (ADR-0047): nothing generates or reads it.
cases=$((cases + 1))
secret_exists defaults "$SIGNING_SECRET" && bad "a shared signing key Secret is still generated"
check "no shared signing key reaches the config" "$(config defaults '.auth.signing_key_secret // "none"')" "none"

# ─── session signing key ─────────────────────────────────────────────────────
# ADR-0047: mounted as a file into the roles that mint sessions and no other,
# and never readable through the API, so no Role names it.

# mounters <name> <secret> — the Deployments that mount that Secret.
mounters() {
    yq -r 'select(.kind == "Deployment") | select(.spec.template.spec.volumes[]?.secret.secretName == "'"$2"'") | .metadata.name' \
        "$scratch/$1.yaml" | sort -u | paste -sd, -
}

render session-default
cases=$((cases + 1))
secret_exists session-default "$SESSION_SECRET" || bad "defaults: no session key Secret was generated"
check "defaults: only iam mounts the session key" "$(mounters session-default "$SESSION_SECRET")" "$RELEASE-iam"
cases=$((cases + 1))
rbac_names session-default | grep -x "$SESSION_SECRET" >/dev/null && bad "defaults: a Role can read the session key"
check "defaults: the generated session key is P-256" \
    "$(yq -r 'select(.kind == "Secret" and .metadata.name == "'"$SESSION_SECRET"'") | .data["session-key.pem"]' "$scratch/session-default.yaml" |
        base64 -d | openssl ec -noout -text 2>/dev/null | sed -n 's/^NIST CURVE: //p')" "P-256"

check "defaults: the minting role reads the mounted session key" \
    "$(config session-default .auth.session_key_path)" "/etc/paladin-session/session-key.pem"
check "defaults: the other roles fetch the iam role's JWKS" \
    "$(config session-default .auth.jwks_url)" "http://$RELEASE-iam:8085/.well-known/jwks.json"
check "defaults: no JWKS CA without internal TLS" "$(config session-default .auth.jwks_ca_path)" ""

render session-tls --set internalTLS.enabled=true --set internalTLS.existingSecret=internal-tls
check "internal TLS: the JWKS is fetched over https" \
    "$(config session-tls .auth.jwks_url)" "https://$RELEASE-iam:8085/.well-known/jwks.json"
check "internal TLS: the JWKS fetch trusts the internal CA" \
    "$(config session-tls .auth.jwks_ca_path)" "$(yq -r .internalTLS.mountPath "$CHART/values.yaml")/ca.crt"

render session-path --set config.auth.session_key_path=/keys/mine.pem
check "a session_key_path set is kept" "$(config session-path .auth.session_key_path)" "/keys/mine.pem"

render session-existing --set auth.sessionKey.existingSecret=my-session-key
cases=$((cases + 1))
secret_exists session-existing "$SESSION_SECRET" && bad "existingSecret: a session key Secret was generated anyway"
check "existingSecret: only iam mounts it" "$(mounters session-existing my-session-key)" "$RELEASE-iam"

render session-off --set auth.sessionKey.generate=false
check "no session key: nothing mounts one" "$(mounters session-off "$SESSION_SECRET")" ""

# ─── RBAC per role ───────────────────────────────────────────────────────────
# ADR-0047: each role's service account reads only the Secrets its config makes
# it read (files/role-secrets.json), and a role reading none gets no Role at
# all — an empty resourceNames would grant every Secret.

# role_names <name> <role> — the Secrets that role's Role names, comma-joined.
role_names() {
    yq -r 'select((.kind == "Role" or .kind == "ClusterRole") and .metadata.name == "'"$RELEASE-secret-reader-$2"'") | .rules[0].resourceNames | join(",")' \
        "$scratch/$1.yaml"
}

render rbac-roles --set config.datastores.valkey.password_secret.name=valkey-pw \
    --set config.middleware.rate_limit.backend=valkey --set config.api_token.enabled=true \
    --set config.mcp.dpop_key_secret.name=bridge-dpop --set rbac.secretReader.secretNames=null
check "the bridge reads its DPoP key alone" "$(role_names rbac-roles mcp)" "bridge-dpop"
check "migrate reads the two Postgres passwords" "$(role_names rbac-roles migrate)" "paladin-postgres-app,paladin-postgres-migrate"
cases=$((cases + 1))
role_names rbac-roles worker | tr ',' '\n' | grep -qx valkey-pw && bad "worker reads Valkey's password, which it never opens"
cases=$((cases + 1))
role_names rbac-roles api | tr ',' '\n' | grep -qx valkey-pw || bad "api cannot read Valkey's password, which its rate limits need"
cases=$((cases + 1))
[[ -z "$(yq -r 'select(.kind == "Role" or .kind == "ClusterRole") | select((.rules[0].resourceNames // []) | length == 0) | select(.metadata.name != "'"$RELEASE-runtime-secret-reader"'") | .metadata.name' "$scratch/rbac-roles.yaml")" ]] ||
    bad "a Role names no Secret, which grants every Secret"
cases=$((cases + 1))
rbac_names rbac-roles | grep -qx "$SESSION_SECRET" && bad "a Role names the session key"

# The OAuth seed clients are seeded by the iam role alone (ADR-0047).
render rbac-oauth --set config.auth.oauth.enabled=true \
    --set 'config.auth.oauth.seed_clients[0].client_id=c' \
    --set 'config.auth.oauth.seed_clients[0].client_secret_ref.name=seed-client' \
    --set 'config.auth.oauth.seed_clients[0].client_secret_ref.key=secret'
cases=$((cases + 1))
role_names rbac-oauth iam | tr ',' '\n' | grep -qx seed-client || bad "iam cannot read the OAuth seed client's secret"
cases=$((cases + 1))
role_names rbac-oauth api | tr ',' '\n' | grep -qx seed-client && bad "api reads the OAuth seed client's secret, which only iam seeds"

render runtime-secrets --set config.runtime_secrets.namespace=paladin-credentials
check "runtime secrets: one Role, in their namespace" \
    "$(yq -r 'select(.kind == "Role" and .metadata.name == "'"$RELEASE-runtime-secret-reader"'") | .metadata.namespace' "$scratch/runtime-secrets.yaml" | grep -v -e '^---$' -e '^$')" \
    "paladin-credentials"
check "runtime secrets: read by admin, bootstrap and the dispatcher" \
    "$(yq -r 'select(.kind == "RoleBinding" and .metadata.name == "'"$RELEASE-runtime-secret-reader"'") | [.subjects[].name] | join(",")' "$scratch/runtime-secrets.yaml" | grep -v -e '^---$' -e '^$')" \
    "$RELEASE-admin,$RELEASE-bootstrap,$RELEASE-dispatcher"
cases=$((cases + 1))
[[ -z "$(yq -r 'select(.metadata.name == "'"$RELEASE-runtime-secret-reader"'") | .metadata.name' "$scratch/defaults.yaml")" ]] ||
    bad "defaults: a runtime-secret Role with no namespace to confine it"

# ─── API-token digest pepper ─────────────────────────────────────────────────
# A secret of its own, never the session key (ADR-0047): generated when API
# tokens are on and nothing names one, absent while they are off, and left
# alone when the overlay names one.

render pepper-off
cases=$((cases + 1))
secret_exists pepper-off "$PEPPER_SECRET" && bad "api tokens off: a pepper Secret was generated anyway"
check "api tokens off: no pepper ref added" "$(config pepper-off '.api_token.hmac_key_secret // "none"')" "none"

render pepper-on --set config.api_token.enabled=true
cases=$((cases + 1))
secret_exists pepper-on "$PEPPER_SECRET" || bad "api tokens on: no pepper Secret was generated"
check "api tokens on: config reads the generated pepper" "$(config pepper-on .api_token.hmac_key_secret.name)" "$PEPPER_SECRET"
cases=$((cases + 1))
rbac_names pepper-on | grep -x "$PEPPER_SECRET" >/dev/null || bad "api tokens on: RBAC cannot read $PEPPER_SECRET"

render pepper-configured --set config.api_token.enabled=true \
    --set config.api_token.hmac_key_secret.name=my-pepper --set config.api_token.hmac_key_secret.key=hmac_key
cases=$((cases + 1))
secret_exists pepper-configured "$PEPPER_SECRET" && bad "configured pepper: a pepper Secret was generated anyway"
check "configured pepper: config keeps it" "$(config pepper-configured .api_token.hmac_key_secret.name)" "my-pepper"
cases=$((cases + 1))
rbac_names pepper-configured | grep -x my-pepper >/dev/null || bad "configured pepper: RBAC cannot read my-pepper"

render pepper-existing --set config.api_token.enabled=true --set apiToken.hmacKey.existingSecret=their-pepper
cases=$((cases + 1))
secret_exists pepper-existing "$PEPPER_SECRET" && bad "existingSecret pepper: a pepper Secret was generated anyway"
check "existingSecret pepper: config reads it" "$(config pepper-existing .api_token.hmac_key_secret.name)" "their-pepper"

render replaced-list --set 'rbac.secretReader.secretNames={only-this}'
cases=$((cases + 1))
rbac_names replaced-list | grep -x paladin-bootstrap-admin >/dev/null ||
    bad "a replaced rbac.secretReader.secretNames dropped the bootstrap admin Secret"

# A confidential OAuth client's secret, in both #SecretRef spellings.
cat >"$scratch/oauth-clients-values.yaml" <<'VALUES'
config:
  auth:
    oauth:
      seed_clients:
        - client_id: mcp
          public: false
          client_secret_ref: {name: mcp-oauth-object, key: client-secret}
        - client_id: other
          public: false
          client_secret_ref: mcp-oauth-string
        - client_id: agent
  mcp:
    oauth:
      exchange:
        client_id: mcp
        client_secret_ref: {name: mcp-exchange-secret, key: client-secret}
VALUES
render oauth-clients -f "$scratch/oauth-clients-values.yaml"
for name in mcp-oauth-object mcp-oauth-string mcp-exchange-secret; do
    cases=$((cases + 1))
    rbac_names oauth-clients | grep -x "$name" >/dev/null || bad "oauth seed client: RBAC cannot read $name"
done

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
check "postgres: namespaced Roles by default" \
    "$(yq -r 'select(.kind == "Role" or .kind == "ClusterRole") | .kind' "$scratch/defaults.yaml" | grep -v '^---$' | sort -u)" "Role"

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
# An overlay still naming the HS256 key is refused rather than ignored.
refuse legacy-signing "additional properties 'signingKey' not allowed" \
    -f "$CHART/$REQUIRED_VALUES" --set auth.signingKey.existingSecret=my-signing-key
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
for role in api iam admin mcp; do
    check "tls on: $role mounts the certificate" \
        "$(deployment tls "$role" '.spec.template.spec.volumes[] | select(.name == "internal-tls") | .secret.secretName')" "planes-tls"
done
check "tls on: worker does not mount it" \
    "$(deployment tls worker '[.spec.template.spec.volumes[] | select(.name == "internal-tls")] | length')" "0"
check "tls on: api probe over HTTPS" "$(deployment tls api '.spec.template.spec.containers[0].readinessProbe.httpGet.scheme')" "HTTPS"
check "tls on: iam probe over HTTPS" "$(deployment tls iam '.spec.template.spec.containers[0].readinessProbe.httpGet.scheme')" "HTTPS"
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
check "console: iam URL is the iam role's" "$(console_env defaults PALADIN_IAM_URL)" \
    "http://paladin-core-iam.$NAMESPACE.svc.cluster.local:8085"
# The console's server reaches iam alone (the OAuth consent relay); nothing
# else names a plane or carries the health token (ADR-0048).
check "console: names no plane but iam" \
    "$(yq ea -r '[select(.kind == "Deployment")] | .[0] | .spec.template.spec.containers[0].env[].name | select(test("^PALADIN_.*_URL$|HEALTH"))' "$scratch/console-defaults.yaml" | tr '\n' ' ')" \
    "PALADIN_IAM_URL "

# /session/ goes to the session role's Service on the console's own hosts
# (ADR-0048): the default release's, unless named otherwise.
readonly CONSOLE_HOST=console.example
readonly SESSION_SERVICE=paladin-core-session
console session-on --set "traefik.hosts[0]=$CONSOLE_HOST" --set ingress.enabled=true --set "ingress.hosts[0]=$CONSOLE_HOST"
check "console: IngressRoute sends /session/ to the session role" \
    "$(yq ea -r '[select(.kind == "IngressRoute")] | .[0].spec.routes[0] | .match + " " + .services[0].name' "$scratch/console-session-on.yaml")" \
    "Host(\`$CONSOLE_HOST\`) && PathPrefix(\`/session/\`) $SESSION_SERVICE"
check "console: Ingress sends /session to the session role" \
    "$(yq ea -r '[select(.kind == "Ingress")] | .[0].spec.rules[0].http.paths[0] | .path + " " + .backend.service.name' "$scratch/console-session-on.yaml")" \
    "/session $SESSION_SERVICE"

# The CSP allows the storage origins this names and no other; none by default.
readonly STORAGE_A=https://s3.example.com
readonly STORAGE_B=http://localhost:9000
check "console: no storage origin by default" "$(console_env defaults PALADIN_STORAGE_ORIGINS)" ""
console storage --set "storageOrigins={$STORAGE_A,$STORAGE_B}"
check "console: storage origins reach the CSP" "$(console_env storage PALADIN_STORAGE_ORIGINS)" "$STORAGE_A,$STORAGE_B"
cases=$((cases + 1))
if helm template paladin-console "$CONSOLE_CHART" --set "storageOrigins={$STORAGE_A/bucket}" >/dev/null 2>&1; then
    bad "console: a storage origin with a path was not refused"
fi

# The platform banner reaches the BFF's environment, which /api/shell reads on
# every poll; none is set by default.
check "console: no banner by default" "$(console_env defaults PALADIN_BANNER_CONTENT)" ""
console banner --set "banner.content=Maintenance tonight" --set banner.severity=warning \
    --set banner.permanent=true --set banner.position=both
check "console: banner content" "$(console_env banner PALADIN_BANNER_CONTENT)" "Maintenance tonight"
check "console: banner severity" "$(console_env banner PALADIN_BANNER_SEVERITY)" "warning"
check "console: banner permanent" "$(console_env banner PALADIN_BANNER_PERMANENT)" "true"
check "console: banner position" "$(console_env banner PALADIN_BANNER_POSITION)" "both"
cases=$((cases + 1))
if helm template paladin-console "$CONSOLE_CHART" --set banner.severity=urgent >/dev/null 2>&1; then
    bad "console: an unknown banner severity was not refused"
fi

# Readiness asks the console process, not the role fan-out. /api/health/all
# feeds the /health page and answers 200 whatever the roles say, so as a probe
# it never measured the upstreams — only whether six fetches fit the kubelet's
# 1s default. Under load they did not, the only console replica left the
# Service, and every page was a bare 503 from the ingress.
check "console: readiness probes the console itself" \
    "$(yq ea -r '[select(.kind == "Deployment")] | .[0] | .spec.template.spec.containers[0].readinessProbe.httpGet.path' "$scratch/console-defaults.yaml")" \
    "/api/health/live"

# console_container <render> <yq path> — a field of the console container.
console_container() {
    yq ea -r "[select(.kind == \"Deployment\")] | .[0] | .spec.template.spec.containers[0] | $2" "$scratch/console-$1.yaml"
}

# The probes name their own timeout: at the kubelet's 1s default a busy event
# loop missed them, and the only replica left the Service mid-run.
readonly CONSOLE_READINESS_TIMEOUT=3
readonly CONSOLE_LIVENESS_TIMEOUT=5
readonly CONSOLE_CPU_LIMIT=2000m
check "console: readiness timeout" "$(console_container defaults .readinessProbe.timeoutSeconds)" "$CONSOLE_READINESS_TIMEOUT"
check "console: liveness timeout" "$(console_container defaults .livenessProbe.timeoutSeconds)" "$CONSOLE_LIVENESS_TIMEOUT"
# At 500m a full e2e run throttled the console in half of its CFS periods.
check "console: CPU limit" "$(console_container defaults .resources.limits.cpu)" "$CONSOLE_CPU_LIMIT"

console other-release --set backend.release=pic --set backend.namespace=platform \
    --set ingress.enabled=true --set "ingress.hosts[0]=console.example"
check "console: release without the chart name is suffixed" "$(console_env other-release PALADIN_IAM_URL)" \
    "http://pic-paladin-core-iam.platform.svc.cluster.local:8085"
check "console: the session route follows the backend release" \
    "$(yq ea -r '[select(.kind == "Ingress")] | .[0] | .spec.rules[0].http.paths[0].backend.service.name' "$scratch/console-other-release.yaml")" \
    "pic-paladin-core-session"

console explicit --set backend.urls.iam=https://iam.example
check "console: an explicit URL wins" "$(console_env explicit PALADIN_IAM_URL)" "https://iam.example"

console tls --set backend.tls=true --set backend.caSecret.name=planes-ca
check "console: tls dials iam over https" "$(console_env tls PALADIN_IAM_URL)" \
    "https://paladin-core-iam.$NAMESPACE.svc.cluster.local:8085"
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
# ingress_port <name> <path> — the port the Ingress sends that path to.
ingress_port() { ingress "$1" '.spec.rules[0].http.paths[] | select(.path == "'"$2"'") | .backend.service.port.number'; }
check "ingress: OAuth to the iam port" "$(ingress_port ingress /oauth)" "8085"
check "ingress: OAuth metadata to the iam port" "$(ingress_port ingress /.well-known/oauth-authorization-server)" "8085"
check "ingress: the session JWKS to the iam port" "$(ingress_port ingress /.well-known/jwks.json)" "8085"
# ingress_service <name> <path> — the Service the Ingress sends that path to.
ingress_service() { ingress "$1" '.spec.rules[0].http.paths[] | select(.path == "'"$2"'") | .backend.service.name'; }
check "ingress: IAM to the iam role" "$(ingress_service ingress /paladin.iam.v1.*)" "$RELEASE-iam"
check "ingress: the data plane to the api role" "$(ingress_service ingress /)" "$RELEASE-api"
check "ingress: the rest to the data port" "$(ingress_port ingress /)" "8080"
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
    "$(yq ea -r '[select(.kind == "Ingress")] | .[0] | .spec.rules[0].http.paths[] | select(.path == "/") | .backend.service.port.number' "$scratch/console-console-ingress.yaml")" "3000"

# ─── Traefik routes ──────────────────────────────────────────────────────────

# The OAuth Authorization Server is served by the IAM listener outside the
# procedure prefix. Without its own rule /oauth/* fell through to the data
# plane, which does not serve it.
render traefik --set traefik.enabled=true --set 'traefik.hosts={api.example.com}'
# route_port <name> <match> — the port of the IngressRoute rule with that match.
route_port() {
    yq ea -r '[select(.kind == "IngressRoute")] | .[0] | .spec.routes[] | select(.match == "'"$2"'") | .services[0].port' \
        "$scratch/$1.yaml"
}
check "traefik: OAuth and the session JWKS to the iam port" \
    "$(route_port traefik 'Host(`api.example.com`) && (PathPrefix(`/oauth/`) || Path(`/.well-known/oauth-authorization-server`) || Path(`/.well-known/jwks.json`))')" "8085"
check "traefik: iam RPCs to the iam port" \
    "$(route_port traefik 'Host(`api.example.com`) && PathPrefix(`/paladin.iam.v1.`)')" "8085"
check "traefik: the rest to the data port" "$(route_port traefik 'Host(`api.example.com`)')" "8080"
# route_service <name> <match> — the Service of the IngressRoute rule with that match.
route_service() {
    yq ea -r '[select(.kind == "IngressRoute")] | .[0] | .spec.routes[] | select(.match == "'"$2"'") | .services[0].name' \
        "$scratch/$1.yaml"
}
check "traefik: iam RPCs to the iam role" \
    "$(route_service traefik 'Host(`api.example.com`) && PathPrefix(`/paladin.iam.v1.`)')" "$RELEASE-iam"
check "traefik: the rest to the api role" "$(route_service traefik 'Host(`api.example.com`)')" "$RELEASE-api"

# The MCP server on a host of its own, plain HTTP to the pod; none by default.
mcp_route() {
    yq ea -r '[select(.kind == "IngressRoute" and .metadata.name == "'"$RELEASE"'-mcp")] | .[0] | '"$2" \
        "$scratch/$1.yaml"
}
check "traefik: no MCP route by default" "$(mcp_route traefik '.metadata.name // "none"')" "none"
render traefik-mcp --set traefik.enabled=true --set 'traefik.hosts={api.example.com}' \
    --set 'traefik.mcp.hosts={mcp.example.com}'
check "traefik: MCP host to the MCP port" \
    "$(mcp_route traefik-mcp '.spec.routes[] | select(.match == "Host(`mcp.example.com`)") | .services[0].port')" "8095"
check "traefik: MCP dialled over plain HTTP" \
    "$(mcp_route traefik-mcp '.spec.routes[0].services[0].serversTransport // "none"')" "none"
render mcp-cert --set traefik.enabled=true --set 'traefik.hosts={api.example.com}' \
    --set 'traefik.mcp.hosts={mcp.example.com}' --set tls.certManager.enabled=true
cases=$((cases + 1))
yq -r 'select(.kind == "Certificate") | .spec.dnsNames[]' "$scratch/mcp-cert.yaml" | grep -x mcp.example.com >/dev/null ||
    bad "certificate: the MCP host is not covered"

# ─── rollout history ─────────────────────────────────────────────────────────

# Kubernetes keeps ten old ReplicaSets per Deployment unless told otherwise.
readonly HISTORY_DEFAULT=3
for role in api iam admin worker mcp dispatcher; do
    check "history: $role keeps the default" "$(deployment defaults "$role" .spec.revisionHistoryLimit)" "$HISTORY_DEFAULT"
done
# 0 is a limit, not "unset": a per-role override of 0 must survive.
render history-zero --set deployments.worker.revisionHistoryLimit=0
check "history: a role's 0 is kept" "$(deployment history-zero worker .spec.revisionHistoryLimit)" "0"
check "history: other roles keep the default" "$(deployment history-zero api .spec.revisionHistoryLimit)" "$HISTORY_DEFAULT"
check "history: console keeps the default" \
    "$(yq ea -r '[select(.kind == "Deployment")] | .[0] | .spec.revisionHistoryLimit' "$scratch/console-defaults.yaml")" "$HISTORY_DEFAULT"

# The console's session role (ADR-0048) runs by default: it needs no origin
# configured, since it accepts the host a call arrives on.
readonly SESSION_ORIGIN=https://console.example
check "session: deployed by default" "$(deployment defaults session .metadata.name)" "$RELEASE-session"
check "session: no origin configured by default" "$(config defaults '.session.console_origins | length')" "0"
check "session: admin reads its health" \
    "$(config defaults '.admin.health.roles[] | select(.name == "session") | .url')" "http://$RELEASE-session:8086"
check "session: no database Secret in its Role" "$(role_names defaults session)" ""
render session-origin --set "config.session.console_origins[0]=$SESSION_ORIGIN"
check "session: a further origin reaches its config" "$(config session-origin '.session.console_origins[0]')" "$SESSION_ORIGIN"

[[ "$fail" == 0 ]] || exit 1
echo "chart defaults: $cases assertions hold"
