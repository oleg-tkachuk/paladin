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
# exchanged for, so the same write is refused. The client and its grants are
# removed on exit.
#
#   KUBE_CONTEXT=orbstack ./scripts/live-mcp-delegation.sh
#
# Runs as `task -t Taskfile.dev.yaml verify:live-mcp`. Not part of verify-all:
# it needs a running cluster. Requires: kubectl, curl, jq, yq, docker,
# openssl, uuidgen.
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
client_id="" token="" oauth_revoke_url=""
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

# cleanup removes the bridge, the agent's grants and client, and the forwards.
cleanup() {
    docker rm -f "$container" >/dev/null 2>&1
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
ok "admin login"

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

echo "== $pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
