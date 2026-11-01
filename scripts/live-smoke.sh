#!/usr/bin/env bash
# live-smoke.sh — check a deployed release against the behaviour its fixes
# promise, end to end through the planes it ships.
#
# The e2e suite drives the console; this drives the APIs directly, for the
# contracts the console does not exercise: who may read the health snapshot,
# what an update mask may name, what a read-only capability may not do, and
# that an event reaches the dispatcher and can be sent again; and the
# capability paths the store's tenant scoping decides: a request ceiling,
# delegation from the admin plane, the Biscuit form, a revoked chain and the
# platform-wide budget summary; and the limes store under it: a revocation
# reaching every api replica before its cache would lapse, the capability
# census and its drill-down, billing by each granularity, a purged tenant's
# token refused as revoked, and the readiness of the limes schema.
#
# It creates its own tenant, bucket, collection, subscription and two
# capabilities, and removes them on exit, the tenant purged with its charges.
# Endpoints and the admin credential come from the cluster, the way
# scripts/e2e-cluster.sh finds them:
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
# The request ceiling the ceiling check spends to the last request, and the
# one a delegated child gets.
readonly CEILING_REQUESTS=3
readonly CHILD_MAX_REQUESTS=2
# The requests a lease holds for the delegated child, and how many of them
# the check spends.
readonly LEASE_REQUESTS=2
readonly LEASE_SPENT=1
readonly CHILD_TTL_SECONDS=600
# The data port of an api pod, and the first local port the pods are forwarded
# to, one each, so a check can reach every replica rather than one the
# Service picks.
readonly API_POD_PORT=8080
readonly API_POD_LOCAL_PORT_BASE="${API_POD_LOCAL_PORT_BASE:-28300}"
# The pods of the api plane, by the chart's component label.
readonly API_POD_SELECTOR=app.kubernetes.io/component=api
# The billing granularities the time series takes, and how far back it reads.
readonly BILLING_GRANULARITIES=(hour day week)
readonly BILLING_LOOKBACK_DAYS=20
# The health snapshot's token, and the component that reports the limes
# schema's version against the build's.
readonly HEALTH_TOKEN_SECRET=paladin-health-token
readonly LIMES_SCHEMA_COMPONENT=postgres-limes-schema
readonly STATUS_HEALTHY=healthy
# A threshold the budget summary filters at, in percent.
readonly SUMMARY_THRESHOLD_PCT=80
# The object every check that needs one uses.
readonly OBJECT_BODY=hello
readonly COLLECTION=docs
# Every upload is bound to its checksum; the smoke object uses SHA-256.
readonly CHECKSUM_ALGORITHM=CHECKSUM_ALGORITHM_SHA256
# Connect error codes, as the JSON protocol spells them.
# The data plane's own operation for minting a presigned URL, as
# sdk/testdata/capability_ops.json names it; limes defines no bare "presign".
readonly OP_OBJECT_PRESIGN=object:presign
readonly OP_BARE_PRESIGN=presign
# What an Issue answer carrying the capability's Biscuit says, in JSON.
readonly BISCUIT_STATUS_ISSUED=BISCUIT_STATUS_ISSUED
readonly CODE_INVALID_ARGUMENT=invalid_argument
readonly CODE_PERMISSION_DENIED=permission_denied
readonly CODE_NOT_FOUND=not_found
readonly CODE_RESOURCE_EXHAUSTED=resource_exhausted
readonly CODE_FAILED_PRECONDITION=failed_precondition
# The ErrorInfo reason a revoked capability is refused with.
readonly REASON_CAPABILITY_REVOKED=ERROR_REASON_CAPABILITY_REVOKED
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
pod_forwards=()
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
# admin_cap_rpc <capability> <procedure> <body>: as the admin, presenting a
# capability, as Delegate takes one.
admin_cap_rpc() { curl -sk "$admin/paladin.admin.v1.$2" -H 'Content-Type: application/json' \
    -H "Authorization: Bearer $token" -H "X-Paladin-Capability: $1" -H "Idempotency-Key: $(uuidgen)" -d "$3"; }
# reason_of is the ErrorInfo reason of a refusal, or its code without one.
reason_of() { printf %s "$1" | jq -r '.details[0].debug.reason // .code // "ok"' 2>/dev/null || echo unparsable; }

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
            note "delete bucket" "$(admin_rpc BucketService/DeleteBucket "{\"name\":\"$name\",\"skipVersionCheck\":true,\"deleteOnBackend\":true}")"
            gone=""
            for _ in $(seq "$DELETION_POLLS"); do
                if [[ "$(code_of "$(admin_rpc BucketService/GetBucket "{\"name\":\"$name\"}")")" == "$CODE_NOT_FOUND" ]]; then
                    gone=1
                    break
                fi
                sleep "$POLL_SECONDS"
            done
            # A bucket left behind holds SeaweedFS volume slots for good; say so.
            [[ -n "$gone" ]] || echo "!!! bucket $bucket still exists after its delete" >&2
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
    if [[ -n "${token:-}" && -n "${purged_slug:-}" ]]; then
        rv=$(admin_rpc TenantService/GetTenant "{\"name\":\"tenants/$purged_slug\"}" | jq -r '.resourceVersion // empty')
        [[ -n "$rv" ]] && admin_rpc TenantService/DeleteTenant "{\"name\":\"tenants/$purged_slug\",\"resourceVersion\":\"$rv\"}" >/dev/null &&
            admin_rpc TenantService/PurgeTenant "{\"name\":\"tenants/$purged_slug\"}" >/dev/null
    fi
    [[ -n "$forward_pid" ]] && kill "$forward_pid" 2>/dev/null
    for p in "${pod_forwards[@]+"${pod_forwards[@]}"}"; do kill "$p" 2>/dev/null; done
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
# The owner is the tenant's UUID: a slug is refused (it used to be dropped,
# leaving the bucket shared), and the answer is checked so a refusal names
# itself rather than surfacing as a bucket that never provisions.
r=$(admin_rpc BucketService/CreateBucket "{\"parent\":\"storageBackends/$BACKEND\",\"bucketId\":\"$bucket\",\"provisionOnBackend\":true,\"bucket\":{\"ownerTenantId\":\"$tenant\"}}")
expect "bucket created, owned by the tenant" "$r" test "$(printf %s "$r" | jq -r '.ownerTenantId // empty')" = "$tenant"
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
        '{subject:{kind:"PRINCIPAL_KIND_AGENT",tenantId:$t,subject:$s,agentType:"live-smoke"},audience:["data"],caveats:{ops:$ops,maxRequests:$max,unlimitedBudget:{}},ttlSeconds:$ttl}')"
}
r=$(issue live-smoke-writer "[\"put\",\"get\",\"list\",\"$OP_OBJECT_PRESIGN\",\"delete\"]")
writer=$(printf %s "$r" | jq -r '.token // empty')
capabilities+=("$(printf %s "$r" | jq -r '.capability.id // empty')")
r=$(issue live-smoke-bare-op "[\"$OP_BARE_PRESIGN\"]")
expect "an operation limes no longer defines is refused" "$r" test "$(code_of "$r")" = "$CODE_INVALID_ARGUMENT"
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

# ── a request ceiling, delegation, the Biscuit form and a revoked chain ─────
# Each capability is also for the admin plane, where Delegate takes it.
issue_for() {
    admin_rpc CapabilityService/Issue "$(jq -cn --arg t "$tenant" --arg s "$1" --argjson ops "$2" \
        --argjson ttl "$CAPABILITY_TTL_SECONDS" --argjson max "$3" \
        '{subject:{kind:"PRINCIPAL_KIND_AGENT",tenantId:$t,subject:$s,agentType:"live-smoke"},audience:["data","admin"],caveats:{ops:$ops,maxRequests:$max,unlimitedBudget:{}},ttlSeconds:$ttl}')"
}
list_with() { cap_rpc "$1" ObjectService/ListObjects "{\"parent\":\"tenants/$tenant/collections/$COLLECTION\"}"; }
delegate() {
    admin_cap_rpc "$1" CapabilityService/Delegate "$(jq -cn --arg p "$2" --arg t "$tenant" \
        --argjson max "$CHILD_MAX_REQUESTS" --argjson ttl "$CHILD_TTL_SECONDS" \
        '{parentId:$p,subject:{kind:"PRINCIPAL_KIND_AGENT",tenantId:$t,subject:"live-smoke-child",agentType:"live-smoke"},audience:["data"],caveats:{ops:["list"],maxRequests:$max},ttlSeconds:$ttl}')"
}

r=$(issue_for live-smoke-ceiling '["list"]' "$CEILING_REQUESTS")
capped=$(printf %s "$r" | jq -r '.token // empty')
capped_id=$(printf %s "$r" | jq -r '.capability.id // empty')
capabilities+=("$capped_id")
admitted=0
for _ in $(seq "$CEILING_REQUESTS"); do
    [[ "$(code_of "$(list_with "$capped")")" == ok ]] && admitted=$((admitted + 1))
done
expect "a ceiling of $CEILING_REQUESTS admits $admitted" "admitted=$admitted" test "$admitted" -eq "$CEILING_REQUESTS"
r=$(list_with "$capped")
expect "the request past the ceiling is refused" "$r" test "$(code_of "$r")" = "$CODE_RESOURCE_EXHAUSTED"
r=$(admin_rpc CapabilityService/GetUsage "{\"id\":\"$capped_id\"}")
expect "usage counts the admitted requests" "$r" test "$(printf %s "$r" | jq -r '.requestCount // "0"')" = "$CEILING_REQUESTS"

r=$(issue_for live-smoke-parent '["get","list","share"]' "$CAPABILITY_MAX_REQUESTS")
parent=$(printf %s "$r" | jq -r '.token // empty')
parent_id=$(printf %s "$r" | jq -r '.capability.id // empty')
parent_biscuit=$(printf %s "$r" | jq -r '.biscuit // empty')
capabilities+=("$parent_id")
expect "the parent has a Biscuit form" "$r" test -n "$parent_biscuit"
r=$(delegate "$parent" "$parent_id")
child=$(printf %s "$r" | jq -r '.token // empty')
expect "delegate from the admin plane" "$r" test -n "$child"
r=$(list_with "$child")
expect "the child is admitted" "$r" test "$(code_of "$r")" = ok
r=$(list_with "$parent_biscuit")
expect "the Biscuit form is admitted" "$r" test "$(code_of "$r")" = ok
r=$(issue_for live-smoke-noshare '["get","list"]' "$CAPABILITY_MAX_REQUESTS")
noshare=$(printf %s "$r" | jq -r '.token // empty')
noshare_id=$(printf %s "$r" | jq -r '.capability.id // empty')
noshare_biscuit=$(printf %s "$r" | jq -r '.biscuit // empty')
capabilities+=("$noshare_id")
expect "a capability without share has a Biscuit form" "$r" test "$(printf %s "$r" | jq -r '.biscuitStatus // empty')" = "$BISCUIT_STATUS_ISSUED"
r=$(list_with "$noshare_biscuit")
expect "its Biscuit form is admitted" "$r" test "$(code_of "$r")" = ok
r=$(delegate "$noshare" "$noshare_id")
expect "delegate without share is refused" "$r" test "$(code_of "$r")" = "$CODE_PERMISSION_DENIED"
r=$(admin_cap_rpc "" CapabilityService/Delegate "{\"parentId\":\"$parent_id\"}")
expect "delegate with no capability presented is refused" "$r" test "$(code_of "$r")" = "$CODE_FAILED_PRECONDITION"

r=$(admin_rpc CapabilityService/Revoke "{\"id\":\"$parent_id\",\"reason\":\"live smoke\"}")
expect "revoke the parent" "$r" test "$(code_of "$r")" = ok
r=$(list_with "$child")
expect "the child of a revoked parent is refused" "$r" test "$(reason_of "$r")" = "$REASON_CAPABILITY_REVOKED"
r=$(list_with "$parent_biscuit")
expect "the Biscuit of a revoked capability is refused" "$r" test "$(reason_of "$r")" = "$REASON_CAPABILITY_REVOKED"

# ── a budget lease holds a chain's requests and settles what it spent ───────
r=$(issue_for live-smoke-lessor '["list","share"]' "$CAPABILITY_MAX_REQUESTS")
lessor=$(printf %s "$r" | jq -r '.token // empty')
lessor_id=$(printf %s "$r" | jq -r '.capability.id // empty')
capabilities+=("$lessor_id")
r=$(delegate "$lessor" "$lessor_id")
lessee=$(printf %s "$r" | jq -r '.token // empty')
lessee_id=$(printf %s "$r" | jq -r '.capability.id // empty')
capabilities+=("$lessee_id")
r=$(admin_rpc CapabilityService/Lease "{\"id\":\"$lessee_id\",\"requests\":$LEASE_REQUESTS}")
expect "an operator leases requests to a delegated capability" "$r" \
    test "$(printf %s "$r" | jq -r '.lease.requests // "0"')" = "$LEASE_REQUESTS"
for _ in $(seq "$LEASE_SPENT"); do list_with "$lessee" >/dev/null; done
r=$(admin_rpc CapabilityService/GetLease "{\"id\":\"$lessee_id\"}")
expect "a request under the lease is counted on it" "$r" \
    test "$(printf %s "$r" | jq -r '.lease.requestsSpent // "0"')" = "$LEASE_SPENT"
# The parent's own requests so far, Delegate among them: settling adds the
# lease's to them.
before=$(admin_rpc CapabilityService/GetUsage "{\"id\":\"$lessor_id\"}" | jq -r '.requestCount // "0"')
r=$(admin_rpc CapabilityService/SettleLease "{\"id\":\"$lessee_id\"}")
expect "settling answers what the lease spent" "$r" \
    test "$(printf %s "$r" | jq -r '.lease.requestsSpent // "0"')" = "$LEASE_SPENT"
r=$(admin_rpc CapabilityService/GetLease "{\"id\":\"$lessee_id\"}")
expect "a settled lease is gone" "$r" test "$(code_of "$r")" = "$CODE_NOT_FOUND"
r=$(admin_rpc CapabilityService/GetUsage "{\"id\":\"$lessor_id\"}")
expect "what the lease spent reaches its parent" "$r" \
    test "$(printf %s "$r" | jq -r '.requestCount // "0"')" = "$((before + LEASE_SPENT))"

# The orchestrator presenting its own capability leases for the child it
# delegated, with no operator; one without share cannot lease at all.
r=$(admin_cap_rpc "$lessor" CapabilityService/Lease "{\"id\":\"$lessee_id\",\"requests\":$LEASE_SPENT}")
expect "an orchestrator leases for the capability it delegated" "$r" \
    test "$(printf %s "$r" | jq -r '.lease.requests // "0"')" = "$LEASE_SPENT"
r=$(admin_cap_rpc "$lessor" CapabilityService/SettleLease "{\"id\":\"$lessee_id\"}")
expect "the orchestrator settles that lease" "$r" test "$(code_of "$r")" = ok
r=$(admin_cap_rpc "$noshare" CapabilityService/Lease "{\"id\":\"$noshare_id\",\"requests\":$LEASE_SPENT}")
expect "a capability without share cannot lease" "$r" test "$(code_of "$r")" = "$CODE_PERMISSION_DENIED"

# ── the platform-wide budget summary, with each filter ──────────────────────
for body in '{}' '{"unlimitedOnly":true}' "{\"thresholdPct\":$SUMMARY_THRESHOLD_PCT}"; do
    r=$(admin_rpc TenantBudgetService/Summarize "$body")
    expect "budget summary $body" "$r" test "$(code_of "$r")" = ok
done

# ── a revocation reaches every api replica at once ──────────────────────────
# Each pod caches revocation answers; a revocation clears every replica's
# cache through pgstore's notification, so the token is refused on each right
# after it, not once the cache lapses.
pods=()
while IFS= read -r p; do [[ -n "$p" ]] && pods+=("$p"); done < <("${kube[@]}" get pods -l "$API_POD_SELECTOR" \
    --field-selector=status.phase=Running -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}')
pod_ports=()
port=$API_POD_LOCAL_PORT_BASE
for p in "${pods[@]+"${pods[@]}"}"; do
    "${kube[@]}" port-forward "pod/$p" "$port:$API_POD_PORT" >/dev/null 2>&1 &
    pod_forwards+=($!)
    pod_ports+=("$port")
    port=$((port + 1))
done
for pp in "${pod_ports[@]+"${pod_ports[@]}"}"; do
    for _ in $(seq "$FORWARD_WAIT_SECONDS"); do
        curl -s -o /dev/null --max-time 1 "http://localhost:$pp/readyz" && break
        sleep 1
    done
done
expect "${#pod_ports[@]} api replicas reachable" "pods=${pods[*]-}" test "${#pod_ports[@]}" -ge 1
list_on() { curl -s "http://localhost:$1/paladin.data.v1.ObjectService/ListObjects" -H 'Content-Type: application/json' \
    -H "X-Paladin-Capability: $2" -H "Idempotency-Key: $(uuidgen)" -d "{\"parent\":\"tenants/$tenant/collections/$COLLECTION\"}"; }
r=$(issue_for live-smoke-replicas '["list"]' "$CAPABILITY_MAX_REQUESTS")
spread=$(printf %s "$r" | jq -r '.token // empty')
spread_id=$(printf %s "$r" | jq -r '.capability.id // empty')
capabilities+=("$spread_id")
for pp in "${pod_ports[@]+"${pod_ports[@]}"}"; do
    r=$(list_on "$pp" "$spread")
    expect "replica :$pp admits the live token" "$r" test "$(code_of "$r")" = ok
done
admin_rpc CapabilityService/Revoke "{\"id\":\"$spread_id\",\"reason\":\"live smoke\"}" >/dev/null
for pp in "${pod_ports[@]+"${pod_ports[@]}"}"; do
    r=$(list_on "$pp" "$spread")
    expect "replica :$pp refuses it right after the revocation" "$r" test "$(reason_of "$r")" = "$REASON_CAPABILITY_REVOKED"
done

# ── the capability census counts a revoked lineage ──────────────────────────
stat_of() { admin_rpc SystemService/GetPlatformStats '{}' | jq -r ".rls.capabilities.$1 // \"0\" | tonumber"; }
r=$(issue_for live-smoke-census-parent '["get","list","share"]' "$CAPABILITY_MAX_REQUESTS")
census_parent=$(printf %s "$r" | jq -r '.token // empty')
census_parent_id=$(printf %s "$r" | jq -r '.capability.id // empty')
capabilities+=("$census_parent_id")
r=$(delegate "$census_parent" "$census_parent_id")
expect "delegate a child to count" "$r" test -n "$(printf %s "$r" | jq -r '.token // empty')"
before=$(stat_of revoked)
admin_rpc CapabilityService/Revoke "{\"id\":\"$census_parent_id\",\"reason\":\"live smoke\"}" >/dev/null
after=$(stat_of revoked)
expect "revoking a parent counts it and its child as revoked ($before → $after)" "before=$before after=$after" \
    test "$((after - before))" -eq 2
r=$(admin_rpc SystemService/ListPlatformStatsTenants '{"signal":"PLATFORM_STATS_SIGNAL_CAPABILITIES_EXPIRING"}')
expect "the expiring drill-down names the smoke tenant" "$r" \
    test "$(printf %s "$r" | jq -r --arg t "$tenant" '[.tenants[]? | select(.tenantId == $t)] | length')" -ge 1
r=$(admin_rpc CapabilityService/Get "{\"id\":\"$capped_id\"}")
expect "the admin reads a capability of another tenant" "$r" test "$(code_of "$r")" = ok

# ── billing reads the ledger by each granularity ────────────────────────────
r=$(admin_rpc BillingService/GetTenantSummary "{\"tenantId\":\"$tenant\"}")
expect "billing summary" "$r" test "$(code_of "$r")" = ok
to=$(date -u +%Y-%m-%dT%H:%M:%SZ)
from=$(date -u -v-"${BILLING_LOOKBACK_DAYS}"d +%Y-%m-%dT%H:%M:%SZ 2>/dev/null ||
    date -u -d "-${BILLING_LOOKBACK_DAYS} days" +%Y-%m-%dT%H:%M:%SZ)
for g in "${BILLING_GRANULARITIES[@]}"; do
    r=$(admin_rpc BillingService/GetTenantTimeSeries \
        "{\"tenantId\":\"$tenant\",\"periodStart\":\"$from\",\"periodEnd\":\"$to\",\"granularity\":\"$g\"}")
    expect "billing series by $g" "$r" test "$(code_of "$r")" = ok
done
r=$(admin_rpc BillingService/ListTenantSpend "{\"periodStart\":\"$from\",\"periodEnd\":\"$to\"}")
expect "billing lists every tenant's spend" "$r" \
    test "$(code_of "$r")" = ok -a "$(printf %s "$r" | jq -r '(.tenants // []) | type')" = array

# ── a purged tenant's token is refused as revoked ───────────────────────────
# A tenant of its own, with nothing that would hold its purge back.
purged_new=smoke-p$RANDOM
r=$(admin_rpc TenantService/CreateTenant "{\"tenant\":{\"displayName\":\"Live smoke $purged_new\",\"slug\":\"$purged_new\"}}")
purged_tenant=$(printf %s "$r" | jq -r '.id // .tenantId // (.name // "" | sub("tenants/";""))')
purged_slug=$purged_new
r=$(admin_rpc CapabilityService/Issue "$(jq -cn --arg t "$purged_tenant" --argjson ttl "$CAPABILITY_TTL_SECONDS" \
    '{subject:{kind:"PRINCIPAL_KIND_AGENT",tenantId:$t,subject:"live-smoke-purged",agentType:"live-smoke"},audience:["data"],caveats:{ops:["list"],maxRequests:1000,unlimitedBudget:{}},ttlSeconds:$ttl}')")
doomed=$(printf %s "$r" | jq -r '.token // empty')
list_purged() { cap_rpc "$1" ObjectService/ListObjects "{\"parent\":\"tenants/$purged_tenant/collections/$COLLECTION\"}"; }
r=$(list_purged "$doomed")
expect "the tenant's token works before the purge" "$r" test "$(code_of "$r")" = ok
rv=$(admin_rpc TenantService/GetTenant "{\"name\":\"tenants/$purged_slug\"}" | jq -r '.resourceVersion // empty')
admin_rpc TenantService/DeleteTenant "{\"name\":\"tenants/$purged_slug\",\"resourceVersion\":\"$rv\"}" >/dev/null
r=$(admin_rpc TenantService/PurgeTenant "{\"name\":\"tenants/$purged_slug\"}")
expect "purge the tenant" "$r" test "$(code_of "$r")" = ok
[[ "$(code_of "$r")" == ok ]] && purged_slug=""
r=$(list_purged "$doomed")
expect "the purged tenant's token is refused as revoked" "$r" test "$(reason_of "$r")" = "$REASON_CAPABILITY_REVOKED"

# ── every api replica is ready on the limes schema ──────────────────────────
health_token=$("${kube[@]}" get secret "$HEALTH_TOKEN_SECRET" -o jsonpath='{.data.token}' | base64 -d)
for pp in "${pod_ports[@]+"${pod_ports[@]}"}"; do
    r=$(curl -s "http://localhost:$pp/system/health.json" -H "X-Health-Token: $health_token")
    st=$(printf %s "$r" | jq -r --arg c "$LIMES_SCHEMA_COMPONENT" '[.. | objects | select(.name? == $c) | .status][0] // "absent"')
    expect "replica :$pp $LIMES_SCHEMA_COMPONENT is $st" "$r" test "$st" = "$STATUS_HEALTHY"
done
unset health_token

echo "== $pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
