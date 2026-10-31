#!/usr/bin/env bash
# live-mcp-delegation.sh — check ADR-0041's write path on a deployed release:
# an agent's paladin.write consent travels as a caveat of the capability the
# MCP server exchanges its token for, and a write it relays is audited as the
# user's, relayed by the exchange client.
#
# The cluster's own bridge serves the read_only profile, which exposes no
# write tool, so this runs a second bridge beside it: the deployed image, with
# the agent_safe profile, on the host network, reaching the release's planes
# through port-forwards. Its config is rendered from
# live-mcp-delegation.config.yaml.tmpl with the release's own values.
#
# The agent is real: a client registered the way an MCP agent registers, a
# grant consented to as the bootstrap admin, the code exchanged with PKCE. Its
# write is an upload into a collection no one has, named for the run, which
# the data plane audits and refuses as not found: nothing is reserved. The
# client then revokes the grant, which revokes the capability its token was
# exchanged for, so the same write is refused.
#
# An operator then revokes the agent's read grant from the user's page while
# a hold is open on its capability: the capability is revoked, the hold is
# released at once and the revocation says so in the IAM plane's log. An
# operator revoking one capability on the admin plane releases its hold the
# same way.
# Deleting the client revokes what is left. No metered request leaves a hold
# open long enough to revoke under it, so the check opens one by hand in
# limes's tables, through the database cluster's primary, and reads it back
# through the admin plane: the tenant's budget and its list of holds. The
# client and its grants are removed on exit.
#
#   KUBE_CONTEXT=orbstack ./scripts/live-mcp-delegation.sh
#
# Runs as `task -t Taskfile.dev.yaml verify:live-mcp`. Not part of verify-all:
# it needs a running cluster. Requires: kubectl, curl, jq, yq, docker,
# openssl, uuidgen.
#
# PG_NAMESPACE, PG_CLUSTER and PG_DATABASE name the CloudNativePG cluster and
# database the release keeps its capabilities in.
#
# `--render-config` prints the bridge's config from the environment and stops;
# backend/internal/config/live_mcp_config_test.go loads it against the schema.

set -uo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
readonly CONFIG_TEMPLATE="$script_dir/live-mcp-delegation.config.yaml.tmpl"

# render_config prints the bridge's config, refusing a value the environment
# does not hold rather than rendering an empty key.
render_config() {
    yq '(.. | select(tag == "!!str")) |= envsubst(nu,ne,ff)' "$CONFIG_TEMPLATE"
}

if [[ "${1:-}" == --render-config ]]; then
    render_config
    exit
fi

NAMESPACE="${NAMESPACE:-paladin}"
ADMIN_SUBJECT="${ADMIN_SUBJECT:-admin}"
# The release's config, and the Deployment whose image the bridge runs.
readonly CONFIG_MAP=paladin-core-config
readonly CONFIG_KEY=config.yaml
readonly MCP_DEPLOYMENT=paladin-core-mcp
# The planes' Services and ports, and the local ports they are forwarded to.
readonly ADMIN_SERVICE=paladin-core-admin
readonly ADMIN_SERVICE_PORT=8090
readonly API_SERVICE=paladin-core-api
readonly DATA_SERVICE_PORT=8080
readonly IAM_SERVICE_PORT=8085
readonly ADMIN_LOCAL_PORT="${ADMIN_LOCAL_PORT:-28390}"
readonly DATA_LOCAL_PORT="${DATA_LOCAL_PORT:-28380}"
readonly IAM_LOCAL_PORT="${IAM_LOCAL_PORT:-28385}"
readonly MCP_LOCAL_PORT="${MCP_LOCAL_PORT:-28396}"
readonly LOOPBACK=127.0.0.1
readonly FORWARD_WAIT_SECONDS=15
readonly BRIDGE_WAIT_SECONDS=30
# The profile the cluster's bridge does not serve, with the write tools.
readonly BRIDGE_PROFILE=agent_safe
# The path the bridge serves MCP on, and its RFC 9728 metadata, which answers
# once it is up.
readonly MCP_PATH=/mcp
readonly BRIDGE_READY_PATH=/.well-known/oauth-protected-resource
# The audiences of the admin's own tokens.
readonly AUDIENCE_ADMIN=paladin-admin
readonly AUDIENCE_DATA=paladin-data
readonly AUDIENCE_IAM=paladin-iam
# The consented scopes, as the OAuth server spells them.
readonly SCOPE_READ=paladin.read
readonly SCOPE_WRITE=paladin.write
# The agent's redirect: never followed, the code is read off the 302.
readonly REDIRECT_URI=https://live-mcp-delegation.invalid/callback
readonly CLIENT_NAME=live-mcp-delegation
readonly OAUTH_STATE=live-mcp-delegation
# The MCP protocol version offered; the server answers with the one it speaks.
readonly MCP_PROTOCOL=2025-06-18
# The write the check relays, the RPC behind it, and its one-byte body.
readonly WRITE_TOOL=paladin_upload_object
readonly WRITE_ACTION=/paladin.data.v1.ObjectService/UploadObject
readonly WRITE_BODY=x
readonly WRITE_CONTENT_TYPE=text/plain
# How the bridge renders a plane's answer about a missing collection
# (backend/internal/mcp/errors.go), and what it says when a plane refuses the
# token it sent.
readonly PLANE_NOT_FOUND_PREFIX="not_found: "
readonly TOKEN_REFUSED="access token was refused"
# The reason a plane gives for a capability whose session has ended.
readonly REVOKED_REASON=revoked
readonly AUDIT_PAGE_SIZE=50
readonly AUDIT_POLLS=10
readonly POLL_SECONDS=1
# The AS's RFC 7009 revocation endpoint, under its issuer; the release's
# config names no URL for it (backend/internal/auth/oauth: RevokePath).
readonly OAUTH_REVOKE_PATH=/oauth/revoke
# RFC 8693: the exchange the MCP server makes, for the data plane.
readonly GRANT_TYPE_TOKEN_EXCHANGE=urn:ietf:params:oauth:grant-type:token-exchange
readonly TOKEN_TYPE_ACCESS=urn:ietf:params:oauth:token-type:access_token
readonly EXCHANGE_AUDIENCE=data
# The list leaves revoked and expired capabilities out unless asked for them.
readonly DISPOSITION_REVOKED=CAPABILITY_DISPOSITION_REVOKED
readonly EVERY_DISPOSITION='["CAPABILITY_DISPOSITION_ACTIVE","CAPABILITY_DISPOSITION_EXPIRED","CAPABILITY_DISPOSITION_REVOKED"]'
# A capability's state as the IAM plane lists a user's (UserCapability.State).
readonly USER_STATE_ACTIVE=STATE_ACTIVE
readonly EVERY_USER_STATE='["STATE_ACTIVE","STATE_EXPIRED","STATE_REVOKED"]'
# How many of every tenant's capabilities one page of the platform's index
# reads: enough to hold the ones this run exchanged, which are the newest.
readonly PLATFORM_PAGE_SIZE=100
# The database cluster the release's capabilities live in: limes's tables,
# where the check opens a hold.
readonly PG_NAMESPACE="${PG_NAMESPACE:-database}"
readonly PG_CLUSTER="${PG_CLUSTER:-paladin-postgresql}"
readonly PG_DATABASE="${PG_DATABASE:-paladin}"
readonly PG_PRIMARY_LABEL=cnpg.io/instanceRole=primary
readonly PG_CLUSTER_LABEL=cnpg.io/cluster
readonly PG_CONTAINER=postgres
readonly PG_SUPERUSER=postgres
# limes keeps amounts in nano-units; the admin plane answers in
# google.type.Money, whole units and nanos.
readonly NANOS_PER_UNIT=1000000000
# The hold: one whole unit, as a metered read in flight leaves it, open long
# enough that only the revocation can release it. limes writes a counter with
# no budget in its abstract unit.
readonly HOLD_NANOS=$NANOS_PER_UNIT
readonly HOLD_TTL='1 hour'
readonly HOLD_OP=get
readonly HOLD_ACTOR=live-mcp-delegation
readonly ABSTRACT_UNIT=XXX
# The line a bulk revocation logs (backend/internal/auth: logRevoked), and
# the components whose pods serve the operator's revocations: the IAM plane
# runs in the api component.
readonly BULK_REVOKED_MSG="capabilities revoked in bulk"
# The line the admin plane's Revoke logs (backend/internal/auth:
# ReleaseRevoked), and the capability it issues to revoke: an agent's, for
# as long as the check runs.
readonly CAPABILITY_RELEASED_MSG="holds of a revoked capability released"
readonly SINGLE_SUBJECT=live-mcp-delegation
readonly SINGLE_AGENT_TYPE=live-check
readonly SINGLE_TTL_SECONDS=600
readonly COMPONENT_LABEL=app.kubernetes.io/component
readonly IAM_COMPONENT=api
readonly ADMIN_COMPONENT=admin
readonly HTTP_OK=200
readonly HTTP_CREATED=201
readonly HTTP_FOUND=302

for tool in kubectl curl jq yq docker openssl uuidgen; do
    command -v "$tool" >/dev/null 2>&1 || { echo "!!! $tool is not installed" >&2; exit 1; }
done

kube=(kubectl)
[[ -n "${KUBE_CONTEXT:-}" ]] && kube+=(--context "$KUBE_CONTEXT")
kube+=(-n "$NAMESPACE")

admin="http://$LOOPBACK:$ADMIN_LOCAL_PORT"
mcp="http://$LOOPBACK:$MCP_LOCAL_PORT$MCP_PATH"
run_id=$(uuidgen | tr '[:upper:]' '[:lower:]')
container="live-mcp-delegation-${run_id%%-*}"

work=$(mktemp -d "${TMPDIR:-/tmp}/live-mcp-delegation.XXXXXX")
# The grants' refresh tokens, one per line: grant runs in a subshell, so an
# array it appends to would never reach cleanup.
refresh_file="$work/refresh_tokens"
forwards=()
client_id="" token="" iam_token="" oauth_revoke_url=""
# The capabilities a hold was opened on, whose counters cleanup removes.
fixture_capabilities=()
pass=0
fail=0
ok() { echo "PASS $1"; pass=$((pass + 1)); }
bad() { echo "FAIL $1 :: $(printf %s "$2" | head -c 400)"; fail=$((fail + 1)); }
expect() {
    local name=$1 detail=$2
    shift 2
    if "$@"; then ok "$name"; else bad "$name" "$detail"; fi
}
admin_rpc() { curl -s "$admin/paladin.admin.v1.$1" -H 'Content-Type: application/json' \
    -H "Authorization: Bearer $token" -d "$2"; }
# admin_write is admin_rpc for a write the admin plane takes once per key.
admin_write() { curl -s "$admin/paladin.admin.v1.$1" -H 'Content-Type: application/json' \
    -H "Authorization: Bearer $token" -H "Idempotency-Key: $(uuidgen)" -d "$2"; }
iam_rpc() { curl -s "$IAM_URL/paladin.iam.v1.$1" -H 'Content-Type: application/json' \
    -H "Authorization: Bearer $iam_token" -d "$2"; }
pg=(kubectl)
[[ -n "${KUBE_CONTEXT:-}" ]] && pg+=(--context "$KUBE_CONTEXT")
pg+=(-n "$PG_NAMESPACE")
pg_pod=""
# sql <name=value>...: runs the statements on stdin on the primary as the
# superuser, which RLS does not scope, with each value as a psql variable.
sql() {
    local vars=()
    for v in "$@"; do vars+=(-v "$v"); done
    "${pg[@]}" exec -i "$pg_pod" -c "$PG_CONTAINER" -- \
        psql -U "$PG_SUPERUSER" -d "$PG_DATABASE" -X -q -At -v ON_ERROR_STOP=1 "${vars[@]}"
}

# cleanup removes the bridge, the agent's grants and client, and the forwards.
cleanup() {
    docker rm -f "$container" >/dev/null 2>&1
    # The counters the holds were opened on, once nothing is held or spent.
    for cap in "${fixture_capabilities[@]}"; do
        sql "cap=$cap" <<<"DELETE FROM limes.usage WHERE capability_id = :'cap' AND reserved = 0 AND spent = 0;" ||
            echo "!!! remove the hold's counter of $cap" >&2
    done
    while read -r rt; do
        [[ -n "$rt" && -n "$oauth_revoke_url" ]] || continue
        curl -sk -o /dev/null "$oauth_revoke_url" --data-urlencode "client_id=$client_id" --data-urlencode "token=$rt"
    done < <(cat "$refresh_file" 2>/dev/null)
    if [[ -n "$client_id" && -n "$token" ]]; then
        r=$(admin_rpc OAuthClientService/DeleteOAuthClient "$(jq -cn --arg c "$client_id" '{clientId:$c}')")
        [[ "$(printf %s "$r" | jq -r '.code // "ok"')" == ok ]] || echo "!!! delete OAuth client $client_id: $r" >&2
    fi
    for p in "${forwards[@]+"${forwards[@]}"}"; do kill "$p" 2>/dev/null; done
    rm -rf "$work"
}
trap cleanup EXIT

# ── the release's own values ────────────────────────────────────────────────
if ! release=$("${kube[@]}" get configmap "$CONFIG_MAP" -o json | jq -r --arg k "$CONFIG_KEY" '.data[$k] // empty') ||
    [[ -z "$release" ]]; then
    echo "!!! no $CONFIG_MAP in namespace '$NAMESPACE' — is the release deployed?" >&2
    exit 1
fi
cfg() { printf %s "$release" | yq -r "$1 // \"\""; }
# secret_of <path of a SecretRef>: the value the release reads from it.
secret_of() {
    local name key
    name=$(cfg "$1.name") key=$(cfg "$1.key")
    [[ -n "$name" && -n "$key" ]] || return 1
    "${kube[@]}" get secret "$name" -o json | jq -r --arg k "$key" '.data[$k] // empty' | base64 -d
}

export APP_NAME AUTH_ISSUER OAUTH_ISSUER OAUTH_AUTHORIZE_URL OAUTH_TOKEN_URL OAUTH_REGISTER_URL \
    MCP_RESOURCE_URL EXCHANGE_CLIENT_ID MCP_ADDR MCP_PROFILE ADMIN_URL DATA_URL IAM_URL UNUSED_DSN
APP_NAME=$(cfg .app.name)
AUTH_ISSUER=$(cfg .auth.issuer)
OAUTH_ISSUER=$(cfg .mcp.oauth.authorization_server.issuer)
OAUTH_AUTHORIZE_URL=$(cfg .mcp.oauth.authorization_server.authorization_endpoint)
OAUTH_TOKEN_URL=$(cfg .mcp.oauth.authorization_server.token_endpoint)
OAUTH_REGISTER_URL=$(cfg .mcp.oauth.authorization_server.registration_endpoint)
MCP_RESOURCE_URL=$(cfg .mcp.oauth.resource_url)
EXCHANGE_CLIENT_ID=$(cfg .mcp.oauth.exchange.client_id)
MCP_ADDR="$LOOPBACK:$MCP_LOCAL_PORT"
MCP_PROFILE=$BRIDGE_PROFILE
ADMIN_URL="http://$LOOPBACK:$ADMIN_LOCAL_PORT"
DATA_URL="http://$LOOPBACK:$DATA_LOCAL_PORT"
IAM_URL="http://$LOOPBACK:$IAM_LOCAL_PORT"
# Never dialled: the bridge opens no database.
UNUSED_DSN="postgres://unused@$LOOPBACK/unused"
oauth_revoke_url="$OAUTH_ISSUER$OAUTH_REVOKE_PATH"

if ! render_config >"$work/config.yaml"; then
    echo "!!! the release's config lacks a value the bridge needs (above)" >&2
    exit 1
fi
# The image runs as a non-root user that must read the mounted file.
chmod 0755 "$work"
chmod 0644 "$work/config.yaml"

export PALADIN_AUTH_SIGNING_KEY PALADIN_MCP_OAUTH_EXCHANGE_CLIENT_SECRET
PALADIN_AUTH_SIGNING_KEY=$(secret_of .auth.signing_key_secret || cfg .auth.signing_key)
PALADIN_MCP_OAUTH_EXCHANGE_CLIENT_SECRET=$(secret_of .mcp.oauth.exchange.client_secret_ref ||
    cfg .mcp.oauth.exchange.client_secret)
password=$(secret_of .bootstrap.admin.password_secret || cfg .bootstrap.admin.password)
if [[ -z "$PALADIN_AUTH_SIGNING_KEY" || -z "$PALADIN_MCP_OAUTH_EXCHANGE_CLIENT_SECRET" || -z "$password" ]]; then
    echo "!!! could not read the signing key, the exchange client's secret or the admin's password" >&2
    exit 1
fi

# ── the planes, forwarded ───────────────────────────────────────────────────
"${kube[@]}" port-forward "svc/$ADMIN_SERVICE" "$ADMIN_LOCAL_PORT:$ADMIN_SERVICE_PORT" >/dev/null 2>&1 &
forwards+=($!)
"${kube[@]}" port-forward "svc/$API_SERVICE" "$DATA_LOCAL_PORT:$DATA_SERVICE_PORT" \
    "$IAM_LOCAL_PORT:$IAM_SERVICE_PORT" >/dev/null 2>&1 &
forwards+=($!)
for _ in $(seq "$FORWARD_WAIT_SECONDS"); do
    curl -s -o /dev/null --max-time 1 "$admin/" && curl -s -o /dev/null --max-time 1 "$IAM_URL/" && break
    sleep 1
done

login() {
    jq -cn --arg s "$ADMIN_SUBJECT" --arg p "$password" --arg a "$1" '{subject:$s,password:$p,requestedAudience:$a}' |
        curl -s "$IAM_URL/paladin.iam.v1.AuthService/Login" -H 'Content-Type: application/json' -d @- |
        jq -r '.tokens.accessToken // empty'
}
# claim <jwt> <name>: a claim of a token, unverified — the planes verify it.
claim() {
    local p
    p=$(printf %s "$1" | cut -d. -f2 | tr '_-' '/+')
    while (( ${#p} % 4 )); do p+="="; done
    printf %s "$p" | openssl base64 -d -A | jq -r --arg c "$2" '.[$c] // empty'
}
token=$(login "$AUDIENCE_ADMIN")
if [[ -z "$token" ]]; then
    bad "admin login" "no admin token"
    exit 1
fi
user=$(claim "$token" sub)
tenant=$(claim "$token" tenant)
iam_token=$(login "$AUDIENCE_IAM")
expect "admin login" "no IAM token" test -n "$iam_token"
pg_pod=$("${pg[@]}" get pods -l "$PG_CLUSTER_LABEL=$PG_CLUSTER,$PG_PRIMARY_LABEL" -o name | head -n1)
if [[ -z "$pg_pod" ]]; then
    bad "find the database's primary" "no $PG_CLUSTER primary in namespace '$PG_NAMESPACE'"
    exit 1
fi

# ── the agent_safe bridge, from the deployed image ──────────────────────────
image=$("${kube[@]}" get deploy "$MCP_DEPLOYMENT" -o jsonpath='{.spec.template.spec.containers[0].image}')
if ! docker run -d --name "$container" --network host \
    -v "$work/config.yaml:/app/configs/config.yaml:ro" \
    -e PALADIN_AUTH_SIGNING_KEY -e PALADIN_MCP_OAUTH_EXCHANGE_CLIENT_SECRET \
    "$image" serve mcp --transport=http >/dev/null; then
    bad "start the $BRIDGE_PROFILE bridge from $image" "docker run failed"
    exit 1
fi
c=""
for _ in $(seq "$BRIDGE_WAIT_SECONDS"); do
    c=$(curl -s -o /dev/null -w '%{http_code}' --max-time 1 "http://$MCP_ADDR$BRIDGE_READY_PATH")
    [[ "$c" == "$HTTP_OK" ]] && break
    sleep 1
done
if [[ "$c" != "$HTTP_OK" ]]; then
    bad "the $BRIDGE_PROFILE bridge from $image starts" "$(docker logs "$container" 2>&1 | tail -5)"
    exit 1
fi
ok "the $BRIDGE_PROFILE bridge from $image starts"

# ── the agent: registered, consented to, its code exchanged ─────────────────
r=$(jq -cn --arg n "$CLIENT_NAME" --arg u "$REDIRECT_URI" \
    '{client_name:$n,redirect_uris:[$u],token_endpoint_auth_method:"none"}' |
    curl -sk -w '\n%{http_code}' "$OAUTH_REGISTER_URL" -H 'Content-Type: application/json' -d @-)
client_id=$(printf %s "$r" | sed '$d' | jq -r '.client_id // empty')
expect "an MCP agent registers" "$r" test "$(printf %s "$r" | tail -n1)" = "$HTTP_CREATED" -a -n "$client_id"

verifier="$(uuidgen)$(uuidgen)"
challenge=$(printf %s "$verifier" | openssl dgst -sha256 -binary | openssl base64 -A | tr '+/' '-_' | tr -d '=')
# grant <scopes>: consent as the admin for the MCP resource; prints the
# access token and keeps the refresh token in refresh_file, in grant order.
grant() {
    local loc code body
    loc=$(curl -sk -o /dev/null -w '%{http_code} %{redirect_url}' "$OAUTH_AUTHORIZE_URL" \
        --data-urlencode "client_id=$client_id" --data-urlencode "redirect_uri=$REDIRECT_URI" \
        --data-urlencode "scope=$1" --data-urlencode "resource=$MCP_RESOURCE_URL" \
        --data-urlencode "code_challenge=$challenge" --data-urlencode "code_challenge_method=S256" \
        --data-urlencode "state=$OAUTH_STATE" --data-urlencode "action=allow" \
        --data-urlencode "username=$ADMIN_SUBJECT" --data-urlencode "password=$password" \
        --data-urlencode "tenant=$tenant")
    [[ "${loc%% *}" == "$HTTP_FOUND" ]] || { echo "authorize answered $loc" >&2; return 1; }
    code=$(printf %s "${loc#* }" | sed -n 's/.*[?&]code=\([^&]*\).*/\1/p')
    body=$(curl -sk "$OAUTH_TOKEN_URL" --data-urlencode "grant_type=authorization_code" \
        --data-urlencode "code=$code" --data-urlencode "client_id=$client_id" \
        --data-urlencode "redirect_uri=$REDIRECT_URI" --data-urlencode "code_verifier=$verifier")
    printf '%s\n' "$(printf %s "$body" | jq -r '.refresh_token // empty')" >>"$refresh_file"
    printf %s "$body" | jq -r '.access_token // empty'
}
writer=$(grant "$SCOPE_READ $SCOPE_WRITE")
expect "consent to $SCOPE_READ $SCOPE_WRITE" "no token" test -n "$writer"
reader=$(grant "$SCOPE_READ")
expect "consent to $SCOPE_READ alone" "no token" test -n "$reader"
leftover=$(grant "$SCOPE_READ")
expect "consent to $SCOPE_READ again" "no token" test -n "$leftover"
unset password

# ── one session per grant, one write each ───────────────────────────────────
# mcp_post <token> <session> <body>: headers and body of one JSON-RPC post.
mcp_post() {
    local h=(-H "Authorization: Bearer $1" -H 'Content-Type: application/json'
        -H 'Accept: application/json, text/event-stream')
    [[ -n "$2" ]] && h+=(-H "Mcp-Session-Id: $2" -H "Mcp-Protocol-Version: $MCP_PROTOCOL")
    curl -s -D "$work/headers" "$mcp" "${h[@]}" -d "$3"
}
# rpc_result: the JSON-RPC message of a reply, plain or as server-sent events.
rpc_result() { sed -n 's/^data: *//p' | tail -n1 | grep . || cat; }
open_session() {
    mcp_post "$1" "" "$(jq -cn --arg v "$MCP_PROTOCOL" \
        '{jsonrpc:"2.0",id:1,method:"initialize",params:{protocolVersion:$v,capabilities:{},clientInfo:{name:"live-mcp-delegation",version:"1"}}}')" >/dev/null
    local sid
    sid=$(tr -d '\r' <"$work/headers" | sed -n 's/^[Mm]cp-[Ss]ession-[Ii]d: *//p')
    mcp_post "$1" "$sid" '{"jsonrpc":"2.0","method":"notifications/initialized"}' >/dev/null
    printf %s "$sid"
}
# call <token> <session> <tool> <arguments>: the tool's text, or the error.
call() {
    mcp_post "$1" "$2" "$(jq -cn --arg t "$3" --argjson a "$4" \
        '{jsonrpc:"2.0",id:2,method:"tools/call",params:{name:$t,arguments:$a}}')" | rpc_result |
        jq -r '(.result.content // [] | map(.text // "") | join("")) + (.error.message // "")'
}

marker="live-mcp-${run_id%%-*}"
args=$(jq -cn --arg p "tenants/$tenant/collections/$marker" --arg t "$WRITE_CONTENT_TYPE" \
    --argjson n "${#WRITE_BODY}" \
    --arg c "$(printf %s "$WRITE_BODY" | openssl dgst -sha256 -binary | openssl base64 -A)" \
    '{parent:$p,content_type:$t,size_bytes:$n,checksum_value:$c}')

reading=$(open_session "$reader")
text=$(call "$reader" "$reading" "$WRITE_TOOL" "$args")
expect "a $SCOPE_READ grant may not write" "$text" grep -q "$SCOPE_WRITE" <<<"$text"

writing=$(open_session "$writer")
text=$(call "$writer" "$writing" "$WRITE_TOOL" "$args")
# reached_plane <tool text>: the plane answered for the collection, with the
# token accepted.
reached_plane() { [[ "$1" == "$PLANE_NOT_FOUND_PREFIX"* && "$1" != *"$TOKEN_REFUSED"* ]]; }
expect "a $SCOPE_WRITE grant's write reaches the plane" "$text" reached_plane "$text"

# ── the audit log: the user's write, relayed by the exchange client ─────────
filter="relayed_by == \"$EXCHANGE_CLIENT_ID\" && action == \"$WRITE_ACTION\" && search.contains(\"$marker\")"
entries="[]"
for _ in $(seq "$AUDIT_POLLS"); do
    entries=$(admin_rpc AuditLogService/ListAuditLog \
        "$(jq -cn --arg f "$filter" --argjson n "$AUDIT_PAGE_SIZE" '{filter:$f,page:{pageSize:$n}}')" |
        jq -c '.entries // []')
    [[ "$(jq length <<<"$entries")" -gt 0 ]] && break
    sleep "$POLL_SECONDS"
done
expect "the write is audited once, relayed by $EXCHANGE_CLIENT_ID" "$entries" test "$(jq length <<<"$entries")" -eq 1
expect "the audited actor is the user, on the data plane" "$entries" test \
    "$(jq -r '.[0] | "\(.actorSubject) \(.actorAudience)"' <<<"$entries")" = "$user $AUDIENCE_DATA"
expect "the audited write names the delegated capability" "$entries" test -n "$(jq -r '.[0].capabilityId // empty' <<<"$entries")"

# ── ending the grant's session revokes its capability ───────────────────────
# The writer's grant was the first consented to. Revoking it revokes the
# capability its token was exchanged for at once, though the token itself
# lives on, so the bridge's write with the same token is refused.
writer_refresh=$(sed -n 1p "$refresh_file")
c=$(curl -sk -o /dev/null -w '%{http_code}' "$oauth_revoke_url" \
    --data-urlencode "client_id=$client_id" --data-urlencode "token=$writer_refresh")
expect "the client revokes its $SCOPE_WRITE grant" "answered $c" test "$c" = "$HTTP_OK"
text=$(call "$writer" "$writing" "$WRITE_TOOL" "$args")
expect "the revoked grant's capability no longer reaches the plane" "$text" test "$(reached_plane "$text" && echo reached)" != reached
expect "the refusal says the capability is revoked" "$text" grep -qi "$REVOKED_REASON" <<<"$text"

# ── an operator revoking a grant releases its capabilities' holds ───────────
# exchange <token>: the capability the MCP server exchanges token for.
exchange() {
    curl -sk "$OAUTH_TOKEN_URL" --data-urlencode "grant_type=$GRANT_TYPE_TOKEN_EXCHANGE" \
        --data-urlencode "client_id=$EXCHANGE_CLIENT_ID" \
        --data-urlencode "client_secret=$PALADIN_MCP_OAUTH_EXCHANGE_CLIENT_SECRET" \
        --data-urlencode "subject_token=$1" --data-urlencode "subject_token_type=$TOKEN_TYPE_ACCESS" \
        --data-urlencode "audience=$EXCHANGE_AUDIENCE" | jq -r '.access_token // empty'
}
# session_capabilities <session>: the capabilities the list finds under the
# OAuth session, in every disposition.
session_capabilities() {
    admin_rpc CapabilityService/List "$(jq -cn --arg t "$tenant" --arg s "$1" --argjson d "$EVERY_DISPOSITION" \
        '{tenantId:$t,session:$s,dispositions:$d}')" | jq -c '.capabilities // []'
}
# user_capabilities [grant]: the capabilities the IAM plane lists as acting
# for the admin, in every state — one grant's when it is given.
user_capabilities() {
    iam_rpc UserService/ListUserCapabilities "$(jq -cn --arg n "tenants/$tenant/users/$user" --arg g "${1:-}" \
        --argjson s "$EVERY_USER_STATE" '{name:$n,grantId:$g,states:$s}')" | jq -c '.capabilities // []'
}
# platform_capabilities: the newest page of every tenant's capabilities, as
# the platform's index lists them.
platform_capabilities() {
    admin_rpc CapabilityService/List "$(jq -cn --argjson d "$EVERY_DISPOSITION" --argjson n "$PLATFORM_PAGE_SIZE" \
        '{dispositions:$d,sortOrder:"SORT_ORDER_DESC",pageSize:$n}')" | jq -c '.capabilities // []'
}
# disposition_in_session <session> <capability>: the capability's disposition
# as the list filtered to session reports it; empty if not listed.
disposition_in_session() {
    session_capabilities "$1" | jq -r --arg c "$2" 'map(select(.id == $c)) | .[0].disposition // empty'
}
# logged <component> <since> <message> <jq condition>: whether a pod of the
# component logged the message since then with fields meeting the condition.
logged() {
    local _
    for _ in $(seq "$AUDIT_POLLS"); do
        "${kube[@]}" logs -l "$COMPONENT_LABEL=$1" --since-time="$2" --tail=-1 2>/dev/null |
            jq -Rc 'fromjson? // empty' |
            jq -e --arg m "$3" "select(.msg == \$m and ($4))" >/dev/null && return 0
        sleep "$POLL_SECONDS"
    done
    return 1
}
# money_nanos is the jq definition turning a google.type.Money into nanos.
money_nanos="def nanos: ((.units // \"0\") | tonumber) * $NANOS_PER_UNIT + (.nanos // 0);"
# budget_reserved prints what the tenant's holds keep from its budget, in
# nanos, as the admin plane reports it: nothing when the tenant has no budget.
budget_reserved() {
    local r
    r=$(admin_rpc TenantBudgetService/Get "$(jq -cn --arg t "$tenant" '{tenantId:$t}')")
    jq -r "$money_nanos"' if .code == "not_found" then 0 elif .code then error(.message) else (.budget.reserved // {} | nanos) end' <<<"$r" ||
        echo "$r"
}
# open_hold <capability> <hold id>: opens a hold of HOLD_NANOS on the
# capability in limes's tables, as a metered request in flight leaves it.
open_hold() {
    fixture_capabilities+=("$1")
    sql "cap=$1" "tenant=$tenant" "hold=$2" "amount=$HOLD_NANOS" "ttl=$HOLD_TTL" \
        "op=$HOLD_OP" "actor=$HOLD_ACTOR" "abstract=$ABSTRACT_UNIT" <<'SQL'
BEGIN;
SELECT coalesce((SELECT unit_code FROM limes.tenant_budgets WHERE tenant_id = :'tenant'), :'abstract') AS unit \gset
INSERT INTO limes.usage (capability_id, tenant_id, reserved, unit_code) VALUES (:'cap', :'tenant', :amount, :'unit')
    ON CONFLICT (capability_id) DO UPDATE SET reserved = limes.usage.reserved + :amount;
UPDATE limes.tenant_budgets SET reserved = reserved + :amount WHERE tenant_id = :'tenant';
INSERT INTO limes.reservations (id, capability_id, tenant_id, ancestors, amount, unit_code, op, actor, copies, expires_at)
    VALUES (:'hold', :'cap', :'tenant', '{}', :amount, :'unit', :'op', :'actor', '[]', now() + :'ttl'::interval);
COMMIT;
SQL
}
# expect_held <capability> <hold id>: the hold is listed on the capability
# and counted in the tenant's budget, over budget_before.
expect_held() {
    local got want
    got=$(holds_on "$1")
    want=$(jq -cn --arg h "$2" --argjson n "$HOLD_NANOS" '{id:$h, nanos:$n}')
    expect "the tenant's holds list the hold on the capability" "listed $got" test "$got" = "$want"
    got=$(budget_reserved)
    expect "the tenant's budget counts the hold" "reserved $got, was $budget_before" \
        test "$got" = "$((budget_before + HOLD_NANOS))"
}
# expect_released <capability>: no hold is listed on the capability and the
# tenant's budget holds what it did before.
expect_released() {
    local got
    got=$(holds_on "$1")
    expect "the hold is released at once" "listed $got" test -z "$got"
    got=$(budget_reserved)
    expect "the tenant's budget holds what it did before" "reserved $got, was $budget_before" \
        test "$got" = "$budget_before"
}
# holds_on <capability> prints the tenant's holds on the capability as the
# admin plane lists them, one {id, nanos} per line, through every page.
holds_on() {
    local page="" r
    while :; do
        r=$(admin_rpc TenantBudgetService/ListReservations \
            "$(jq -cn --arg t "$tenant" --arg p "$page" '{tenantId:$t, pageToken:$p}')")
        # An error answers with a code; no holds, with no list at all.
        if jq -e 'has("code")' <<<"$r" >/dev/null; then
            echo "$r"
            return 1
        fi
        jq -c --arg c "$1" "$money_nanos"' .reservations // [] | .[] | select(.capabilityId == $c) | {id, nanos: (.amount | nanos)}' <<<"$r"
        page=$(jq -r '.nextPageToken // empty' <<<"$r")
        [[ -z "$page" ]] && return 0
    done
}

# The bridge exchanged the read grant's token when it relayed the refused
# write; the list finds that capability by the grant's session.
reader_session=$(claim "$reader" sid)
listed=$(session_capabilities "$reader_session")
reader_capability=$(jq -r 'if length == 1 then .[0].id else empty end' <<<"$listed")
if [[ -z "$reader_capability" || "$(jq -r '.[0].disposition' <<<"$listed")" == "$DISPOSITION_REVOKED" ]]; then
    bad "the capability list finds the grant's live capability by its OAuth session" "$listed"
    echo "== $pass passed, $fail failed"
    exit 1
fi
ok "the capability list finds the grant's live capability by its OAuth session"

# The user sees what acts for them, without the admin plane: the grant's
# capability, under its grant, and nothing under a grant they never gave.
mine=$(user_capabilities | jq -c --arg c "$reader_capability" 'map(select(.id == $c)) | .[0] // empty')
expect "the user's capabilities list the grant's capability" "listed $mine" \
    test "$(jq -r '"\(.state) \(.grantId)"' <<<"${mine:-null}")" = "$USER_STATE_ACTIVE $reader_session"
granted=$(user_capabilities "$reader_session")
# shellcheck disable=SC2016 # $c and $g are jq variables, not shell ones
expect "narrowed to the grant, they list its capabilities alone" "listed $granted" \
    jq -e --arg c "$reader_capability" --arg g "$reader_session" \
    'any(.id == $c) and all(.grantId == $g)' <<<"$granted"
other=$(user_capabilities "$(uuidgen | tr '[:upper:]' '[:lower:]')")
expect "narrowed to a grant never given, they list none" "listed $other" test "$other" = "[]"
# The platform's index lists it among every tenant's, naming its tenant.
indexed=$(platform_capabilities | jq -r --arg c "$reader_capability" 'map(select(.id == $c)) | .[0].subject.tenantId // empty')
expect "every tenant's capabilities list it under its tenant" "tenant '$indexed'" test "$indexed" = "$tenant"

budget_before=$(budget_reserved)
hold=$(uuidgen | tr '[:upper:]' '[:lower:]')
open_hold "$reader_capability" "$hold"
expect_held "$reader_capability" "$hold"

since=$(date -u +%Y-%m-%dT%H:%M:%SZ)
r=$(iam_rpc UserService/RevokeOAuthGrant \
    "$(jq -cn --arg n "tenants/$tenant/users/$user" --arg g "$reader_session" '{name:$n,grantId:$g}')")
expect "an operator revokes the $SCOPE_READ grant" "$r" test "$(jq -r '.code // "ok"' <<<"$r")" = ok
d=$(disposition_in_session "$reader_session" "$reader_capability")
expect "the grant's capability is revoked" "disposition '$d'" test "$d" = "$DISPOSITION_REVOKED"
expect_released "$reader_capability"
expect "the IAM plane logs the release" "no '$BULK_REVOKED_MSG' line for session $reader_session" \
    logged "$IAM_COMPONENT" "$since" "$BULK_REVOKED_MSG" \
    ".session == \"$reader_session\" and .capabilities_revoked >= 1 and .holds_released >= 1"

# ── an operator revokes one capability ──────────────────────────────────────
r=$(admin_write CapabilityService/Issue "$(jq -cn --arg t "$tenant" --arg s "$SINGLE_SUBJECT" \
    --arg a "$SINGLE_AGENT_TYPE" --arg aud "$EXCHANGE_AUDIENCE" --arg op "$HOLD_OP" --argjson ttl "$SINGLE_TTL_SECONDS" \
    '{subject:{kind:"PRINCIPAL_KIND_AGENT",tenantId:$t,subject:$s,agentType:$a},audience:[$aud],
      caveats:{ops:[$op],unlimitedRequests:true,unlimitedBudget:{}},ttlSeconds:$ttl}')")
single=$(jq -r '.capability.id // empty' <<<"$r")
expect "an operator issues a capability" "$r" test -n "$single"
budget_before=$(budget_reserved)
hold=$(uuidgen | tr '[:upper:]' '[:lower:]')
open_hold "$single" "$hold"
expect_held "$single" "$hold"
since=$(date -u +%Y-%m-%dT%H:%M:%SZ)
r=$(admin_write CapabilityService/Revoke "$(jq -cn --arg c "$single" '{id:$c, reason:"live check"}')")
expect "an operator revokes the capability" "$r" test "$(jq -r '.code // "ok"' <<<"$r")" = ok
expect_released "$single"
expect "the admin plane logs the release" "no '$CAPABILITY_RELEASED_MSG' line for capability $single" \
    logged "$ADMIN_COMPONENT" "$since" "$CAPABILITY_RELEASED_MSG" \
    ".capability_id == \"$single\" and .holds_released == 1"

# ── deleting the client revokes what is left ────────────────────────────────
leftover_session=$(claim "$leftover" sid)
leftover_capability=$(claim "$(exchange "$leftover")" jti)
expect "the last grant's token is exchanged for a capability" "no capability" test -n "$leftover_capability"
since=$(date -u +%Y-%m-%dT%H:%M:%SZ)
r=$(admin_rpc OAuthClientService/DeleteOAuthClient "$(jq -cn --arg c "$client_id" '{clientId:$c}')")
if [[ "$(jq -r '.code // "ok"' <<<"$r")" == ok ]]; then
    ok "an operator deletes the client"
    # Gone with its grants: nothing left for cleanup to revoke or delete.
    deleted_client=$client_id
    client_id=""
else
    bad "an operator deletes the client" "$r"
    deleted_client=""
fi
d=$(disposition_in_session "$leftover_session" "$leftover_capability")
expect "the deleted client's capability is revoked" "disposition '$d'" test "$d" = "$DISPOSITION_REVOKED"
expect "the admin plane logs the revocation" "no '$BULK_REVOKED_MSG' line for client $deleted_client" \
    logged "$ADMIN_COMPONENT" "$since" "$BULK_REVOKED_MSG" \
    ".client_id == \"$deleted_client\" and .all_tenants == true and .capabilities_revoked >= 1"

echo "== $pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
