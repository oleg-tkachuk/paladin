#!/usr/bin/env bash
# live-smoke.sh — check a deployed release against the behaviour its fixes
# promise, end to end through the planes it ships.
#
# The e2e suite drives the console; this drives the APIs directly, for the
# contracts the console does not exercise: who may read the health snapshot,
# what an update mask may name, what a read-only capability may not do, and
# that an event reaches the dispatcher and can be sent again.
#
# It creates its own tenant, bucket, collection, subscription and two
# capabilities, and removes them on exit — except the tenant itself, which ends
# in the trash: the capability calls are billed, and a tenant with charges is
# kept for them (charges.tenant_id is ON DELETE RESTRICT). Endpoints and the admin credential
# come from the cluster, the way scripts/e2e-cluster.sh finds them:
#
#   KUBE_CONTEXT=orbstack ./scripts/live-smoke.sh
#
# Runs as `task -t Taskfile.dev.yaml verify:live`. Not part of verify-all: it
# needs a running cluster. Requires: kubectl, curl, jq, uuidgen, openssl.

set -uo pipefail

NAMESPACE="${NAMESPACE:-paladin}"
BASE_HOST="${BASE_HOST:-paladin.local}"
API_HOST="${API_HOST:-api.paladin.local}"
ADMIN_SUBJECT="${ADMIN_SUBJECT:-admin}"
BACKEND="${BACKEND:-primary}"
# The admin Service's port, and the local one it is forwarded to: the admin
# plane is not routed through the ingress by default.
readonly ADMIN_SERVICE_PORT=8090
readonly ADMIN_LOCAL_PORT="${ADMIN_LOCAL_PORT:-28290}"
readonly FORWARD_WAIT_SECONDS=15
# Bucket provisioning and event delivery are asynchronous (worker and
# dispatcher ticks); these bound how long the script waits for each.
readonly PROVISION_POLLS=30
# The bucket's backend delete is asynchronous too; the tenant cannot be purged
# while the row still references it.
readonly DELETION_POLLS=30
readonly DELIVERY_POLLS=30
readonly POLL_SECONDS=2
# A capability lives long enough for the run and no longer.
readonly CAPABILITY_TTL_SECONDS=900
readonly CAPABILITY_MAX_REQUESTS=50
# The object every check that needs one uses.
readonly OBJECT_BODY=hello
readonly COLLECTION=docs
# Every upload is bound to its checksum; the smoke object uses SHA-256.
readonly CHECKSUM_ALGORITHM=CHECKSUM_ALGORITHM_SHA256
# Connect error codes, as the JSON protocol spells them.
readonly CODE_INVALID_ARGUMENT=invalid_argument
readonly CODE_PERMISSION_DENIED=permission_denied
readonly CODE_NOT_FOUND=not_found
readonly HTTP_OK=200
readonly HTTP_UNAUTHORIZED=401

for tool in kubectl curl jq uuidgen openssl; do
    command -v "$tool" >/dev/null 2>&1 || { echo "!!! $tool is not installed" >&2; exit 1; }
done

kube=(kubectl)
[[ -n "${KUBE_CONTEXT:-}" ]] && kube+=(--context "$KUBE_CONTEXT")
kube+=(-n "$NAMESPACE")

console="https://${BASE_HOST}"
api="https://${API_HOST}"
admin="http://localhost:${ADMIN_LOCAL_PORT}"

work=$(mktemp -d "${TMPDIR:-/tmp}/live-smoke.XXXXXX")
forward_pid=""
pass=0
fail=0
ok() { echo "PASS $1"; pass=$((pass + 1)); }
bad() { echo "FAIL $1 :: $(printf %s "$2" | head -c 400)"; fail=$((fail + 1)); }
code_of() { printf %s "$1" | jq -r '.code // "ok"' 2>/dev/null || echo unparsable; }
# is_ready <provision state>: the API spells it in either case.
is_ready() { [[ "$(printf %s "$1" | tr '[:lower:]' '[:upper:]')" == *READY* ]]; }
# expect <check> <detail on failure> <condition...>
expect() {
    local name=$1 detail=$2
    shift 2
    if "$@"; then ok "$name"; else bad "$name" "$detail"; fi
}

# rpc <base> <procedure> <body>, as the admin (token) or with a capability.
rpc() { curl -sk "$1/$2" -H 'Content-Type: application/json' -H "Authorization: Bearer $token" \
    -H "Idempotency-Key: $(uuidgen)" -d "$3"; }
admin_rpc() { rpc "$admin" "paladin.admin.v1.$1" "$2"; }
cap_rpc() { curl -sk "$api/paladin.data.v1.$2" -H 'Content-Type: application/json' -H "X-Paladin-Capability: $1" \
    -H "Idempotency-Key: $(uuidgen)" -d "$3"; }

slug="" sub="" collection_name="" object="" bucket="" capabilities=()
# note reports a teardown step that did not succeed, without failing the run.
note() { [[ "$(code_of "$2")" == ok ]] || echo "!!! $1: $(printf %s "$2" | jq -r '.message // .' 2>/dev/null)" >&2; }
# cleanup removes what the run created, children first: the tenant cannot be
# purged while a collection or bucket still references it.
cleanup() {
    if [[ -n "${token:-}" ]]; then
        if [[ -n "$sub" ]]; then
            rv=$(admin_rpc EventSubscriptionService/GetSubscription "{\"name\":\"$sub\"}" | jq -r '.resourceVersion // empty')
            admin_rpc EventSubscriptionService/DeleteSubscription "{\"name\":\"$sub\",\"resourceVersion\":\"$rv\"}" >/dev/null
        fi
        if [[ -n "$object" && -n "${writer:-}" ]]; then
            rv=$(cap_rpc "$writer" ObjectService/GetObject "{\"name\":\"$object\"}" | jq -r '.resourceVersion // empty')
            note "delete object" "$(cap_rpc "$writer" ObjectService/DeleteObject \
                "{\"name\":\"$object\",\"resourceVersion\":\"$rv\",\"permanent\":true}")"
        fi
        if [[ -n "$collection_name" ]]; then
            note "delete collection" "$(admin_rpc CollectionService/DeleteCollection "{\"name\":\"$collection_name\",\"skipVersionCheck\":true}")"
        fi
        if [[ -n "$bucket" ]]; then
            name="storageBackends/$BACKEND/buckets/$bucket"
            admin_rpc BucketService/DeleteBucket "{\"name\":\"$name\",\"skipVersionCheck\":true,\"deleteOnBackend\":true}" >/dev/null
            for _ in $(seq "$DELETION_POLLS"); do
                [[ "$(code_of "$(admin_rpc BucketService/GetBucket "{\"name\":\"$name\"}")")" == "$CODE_NOT_FOUND" ]] && break
                sleep "$POLL_SECONDS"
            done
        fi
        for c in "${capabilities[@]+"${capabilities[@]}"}"; do
            admin_rpc CapabilityService/Revoke "{\"id\":\"$c\",\"reason\":\"live smoke done\"}" >/dev/null
        done
        if [[ -n "$slug" ]]; then
            rv=$(admin_rpc TenantService/GetTenant "{\"name\":\"tenants/$slug\"}" | jq -r '.resourceVersion // empty')
            admin_rpc TenantService/DeleteTenant "{\"name\":\"tenants/$slug\",\"resourceVersion\":\"$rv\"}" >/dev/null
            r=$(admin_rpc TenantService/PurgeTenant "{\"name\":\"tenants/$slug\"}")
            if [[ "$(code_of "$r")" == ok ]]; then
                echo "tenant $slug purged"
            else
                echo "tenant $slug is in the trash: $(printf %s "$r" | jq -r '.message // .')"
            fi
        fi
    fi
    [[ -n "$forward_pid" ]] && kill "$forward_pid" 2>/dev/null
    rm -rf "$work"
}
trap cleanup EXIT

if ! "${kube[@]}" get deploy paladin-core-api >/dev/null 2>&1; then
    echo "no paladin-core-api deployment in namespace '$NAMESPACE' — is the cluster up?" >&2
    exit 1
fi
"${kube[@]}" port-forward svc/paladin-core-admin "${ADMIN_LOCAL_PORT}:${ADMIN_SERVICE_PORT}" >/dev/null 2>&1 &
forward_pid=$!
for _ in $(seq "$FORWARD_WAIT_SECONDS"); do
    curl -s -o /dev/null --max-time 1 "$admin/" && break
    sleep 1
done

password=$("${kube[@]}" get secret paladin-bootstrap-admin -o jsonpath='{.data.password}' | base64 -d)
login() {
    jq -cn --arg s "$ADMIN_SUBJECT" --arg p "$password" --arg a "$1" '{subject:$s,password:$p,requestedAudience:$a}' |
        curl -sk "$api/paladin.iam.v1.AuthService/Login" -H 'Content-Type: application/json' -d @- |
        jq -r '.tokens.accessToken // empty'
}
token=$(login paladin-admin)
if [[ -z "$token" ]]; then
    bad login "no admin token"
    exit 1
fi
ok "admin login"

# ── the health snapshot needs a session; the console's session works ────────
c=$(curl -sk -o /dev/null -w '%{http_code}' "$console/api/health/all")
expect "anonymous health snapshot refused ($c)" "$c" test "$c" = "$HTTP_UNAUTHORIZED"
c=$(jq -cn --arg s "$ADMIN_SUBJECT" --arg p "$password" '{subject:$s,password:$p}' |
    curl -sk -c "$work/jar" -o /dev/null -w '%{http_code}' "$console/api/auth/login" \
        -H "Origin: $console" -H 'Content-Type: application/json' -d @-)
expect "console sign-in ($c)" "$c" test "$c" = "$HTTP_OK"
c=$(curl -sk -b "$work/jar" -o /dev/null -w '%{http_code}' "$console/api/health/all")
expect "health snapshot with a session ($c)" "$c" test "$c" = "$HTTP_OK"
unset password

# ── fixture ─────────────────────────────────────────────────────────────────
new_slug=smoke-t$RANDOM
new_bucket=smoke-b$RANDOM
r=$(admin_rpc TenantService/CreateTenant "{\"tenant\":{\"displayName\":\"Live smoke $new_slug\",\"slug\":\"$new_slug\"}}")
tenant=$(printf %s "$r" | jq -r '.id // .tenantId // (.name // "" | sub("tenants/";""))')
if [[ ! "$tenant" =~ ^[0-9a-f-]{36}$ ]]; then
    bad CreateTenant "$r"
    exit 1
fi
slug=$new_slug
ok "tenant $slug"
bucket=$new_bucket
admin_rpc BucketService/CreateBucket "{\"parent\":\"storageBackends/$BACKEND\",\"bucketId\":\"$bucket\",\"provisionOnBackend\":true,\"bucket\":{\"ownerTenantId\":\"$slug\"}}" >/dev/null
state=""
for _ in $(seq "$PROVISION_POLLS"); do
    state=$(admin_rpc BucketService/GetBucket "{\"name\":\"storageBackends/$BACKEND/buckets/$bucket\"}" | jq -r '.provisionState // ""')
    is_ready "$state" && break
    sleep "$POLL_SECONDS"
done
expect "bucket provisioned" "$state" is_ready "$state"
r=$(admin_rpc CollectionService/CreateCollection "{\"parent\":\"tenants/$slug\",\"collection\":\"$COLLECTION\",\"collectionResource\":{\"bucket\":\"storageBackends/$BACKEND/buckets/$bucket\"}}")
collection_name=$(printf %s "$r" | jq -r '.name // empty')
expect collection "$r" test -n "$collection_name"

# ── subscription edits persist; unknown mask paths are refused ──────────────
# A sink nothing listens on: every delivery fails, which is what the redrive
# below needs.
sink="http://paladin-core-admin.${NAMESPACE}.svc:${ADMIN_SERVICE_PORT}/live-smoke-no-such-sink"
r=$(admin_rpc EventSubscriptionService/CreateSubscription \
    "{\"parent\":\"tenants/$tenant\",\"subscription\":{\"filter\":\"type == 'paladin.none'\",\"sink\":{\"http\":{\"url\":\"$sink\",\"maxAttempts\":1}}}}")
sub=$(printf %s "$r" | jq -r '.name // empty')
rv=$(printf %s "$r" | jq -r '.resourceVersion // empty')
expect "subscription" "$r" test -n "$sub"
filter="type.startsWith('paladin.object.')"
admin_rpc EventSubscriptionService/UpdateSubscription \
    "$(jq -cn --arg n "$sub" --arg rv "$rv" --arg f "$filter" '{name:$n,resourceVersion:$rv,updateMask:"filter",subscription:{filter:$f}}')" >/dev/null
r=$(admin_rpc EventSubscriptionService/GetSubscription "{\"name\":\"$sub\"}")
expect "filter edit persisted" "$r" test "$(printf %s "$r" | jq -r .filter)" = "$filter"
rv=$(printf %s "$r" | jq -r .resourceVersion)
r=$(admin_rpc EventSubscriptionService/UpdateSubscription \
    "{\"name\":\"$sub\",\"resourceVersion\":\"$rv\",\"updateMask\":\"live_smoke_no_such_field\",\"subscription\":{}}")
expect "unknown update_mask path refused" "$r" test "$(code_of "$r")" = "$CODE_INVALID_ARGUMENT"

# ── a read-only capability may read versions but not restore one ────────────
issue() {
    admin_rpc CapabilityService/Issue "$(jq -cn --arg t "$tenant" --arg s "$1" --argjson ops "$2" \
        --argjson ttl "$CAPABILITY_TTL_SECONDS" --argjson max "$CAPABILITY_MAX_REQUESTS" \
        '{subject:{kind:"PRINCIPAL_KIND_AGENT",tenantId:$t,subject:$s,agentType:"live-smoke"},audience:["data"],caveats:{ops:$ops,maxRequests:$max},ttlSeconds:$ttl}')"
}
r=$(issue live-smoke-writer '["put","get","list","presign","delete"]')
writer=$(printf %s "$r" | jq -r '.token // empty')
capabilities+=("$(printf %s "$r" | jq -r '.capability.id // empty')")
r=$(issue live-smoke-reader '["get","list"]')
reader=$(printf %s "$r" | jq -r '.token // empty')
capabilities+=("$(printf %s "$r" | jq -r '.capability.id // empty')")
expect "writer and read-only capabilities" "$r" test -n "$writer" -a -n "$reader"

checksum=$(printf %s "$OBJECT_BODY" | openssl dgst -sha256 -binary | openssl base64 -A)
r=$(cap_rpc "$writer" ObjectService/UploadObject "$(jq -cn --arg p "tenants/$tenant/collections/$COLLECTION" \
    --argjson size "${#OBJECT_BODY}" --arg alg "$CHECKSUM_ALGORITHM" --arg sum "$checksum" \
    '{parent:$p,key:"smoke.txt",contentType:"text/plain",sizeHintBytes:$size,checksumAlgorithm:$alg,checksumValue:$sum}')")
url=$(printf %s "$r" | jq -r '.uploadUrl.url // empty')
object=$(printf %s "$r" | jq -r '.object.name // empty')
# The signature covers the required headers; curl writes Content-Length and
# Host itself, from the body and the URL.
upload_headers=()
while IFS= read -r h; do upload_headers+=(-H "$h"); done < <(printf %s "$r" |
    jq -r '.uploadUrl.requiredHeaders // {} | to_entries[]
        | select(.key | ascii_downcase | IN("content-length", "host") | not) | "\(.key): \(.value)"')
curl -sk -o /dev/null -X PUT "${upload_headers[@]+"${upload_headers[@]}"}" --data-binary "$OBJECT_BODY" "$url"
r=$(cap_rpc "$writer" ObjectService/CompleteObject "{\"name\":\"$object\"}")
object_rv=$(printf %s "$r" | jq -r '.resourceVersion // empty')
expect "object uploaded" "$r" test -n "$object_rv"
r=$(cap_rpc "$reader" ObjectService/ListObjectVersions "{\"parent\":\"$object\"}")
expect "read-only capability lists versions" "$r" test "$(code_of "$r")" = ok
version=$(printf %s "$r" | jq -r '.versions[0].name // empty')
# An unversioned bucket lists none; the refusal comes before the lookup.
[[ -n "$version" ]] || version="$object/versions/$(uuidgen | tr '[:upper:]' '[:lower:]')"
r=$(cap_rpc "$reader" ObjectService/RestoreObjectVersion "{\"name\":\"$version\",\"resourceVersion\":\"$object_rv\"}")
expect "read-only capability may not restore a version" "$r" test "$(code_of "$r")" = "$CODE_PERMISSION_DENIED"

# ── the upload's event reaches the dispatcher; a redrive sends it again ─────
failed=0
for _ in $(seq "$DELIVERY_POLLS"); do
    failed=$(admin_rpc SystemService/GetDispatcherStats '{}' |
        jq -r --arg s "${sub##*/}" '[.subscriptions[]? | select(.subscriptionId == $s) | (.failed // "0" | tonumber)] | add // 0')
    [[ "$failed" -ge 1 ]] && break
    sleep "$POLL_SECONDS"
done
expect "dispatcher attempted the event ($failed failed against the dead sink)" "failed=$failed" test "$failed" -ge 1
r=$(admin_rpc EventSubscriptionService/RedriveFailedDeliveries "{\"name\":\"$sub\"}")
n=$(printf %s "$r" | jq -r '(.requeued // "0") | tonumber' 2>/dev/null || echo 0)
expect "redrive queued $n" "$r" test "$n" -ge 1

echo "== $pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
