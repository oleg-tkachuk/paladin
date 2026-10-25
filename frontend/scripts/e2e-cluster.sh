#!/usr/bin/env bash
# Run the Playwright suite against the deployed cluster instead of the
# disposable compose stack.
#
# The suite reads every endpoint and credential from the environment
# (tests/e2e/fixtures/credentials.ts, fixtures/seed.ts), with defaults pointed
# at the compose stack so an unconfigured run cannot reach anything real. This
# script fills those in from the live cluster.
#
# Two things differ from the compose stack and are easy to get wrong by hand:
#
#   - the admin plane is not routed through the ingress — the chart's default
#     and the local cluster's both leave it internal — so this script reaches
#     it through a port-forward unless ADMIN_URL names a route that exists;
#   - the bootstrap admin's password lives in a Secret, and its subject is
#     `admin`, not the compose fixture's e2e-admin@local — the weak-secret
#     deny-list refuses that one outside a disposable environment.
#
# Usage (from frontend/, or as frontend/scripts/e2e-cluster.sh from anywhere):
#   ./scripts/e2e-cluster.sh                    # whole suite
#   ./scripts/e2e-cluster.sh tests/e2e/x.spec.ts   # one file
#   NAMESPACE=other KUBE_CONTEXT=orbstack ./scripts/e2e-cluster.sh --headed
#
# Fixtures tear themselves down (tests/e2e/fixtures/resources.ts), including
# rows a test created through the console's own dialogs. A run against the
# cluster should leave it exactly as it found it; if it does not, the teardown
# says which resource it could not remove and why.
set -euo pipefail

NAMESPACE="${NAMESPACE:-paladin}"
BASE_HOST="${BASE_HOST:-paladin.local}"
API_HOST="${API_HOST:-api.paladin.local}"
ADMIN_SUBJECT="${ADMIN_SUBJECT:-admin}"
# The admin Service's port, and the local one it is forwarded to.
readonly ADMIN_SERVICE_PORT=8090
readonly ADMIN_LOCAL_PORT="${ADMIN_LOCAL_PORT:-28190}"
# The MCP Service's port and the local one it is forwarded to: like the admin
# plane, the MCP server has no route through the ingress here.
readonly MCP_SERVICE_PORT=8095
readonly MCP_LOCAL_PORT="${MCP_LOCAL_PORT:-28195}"
readonly MCP_PATH=/mcp
# How long the forward gets to start answering.
readonly FORWARD_WAIT_SECONDS=15
readonly REACH_TIMEOUT_SECONDS=8

kube=(kubectl)
[[ -n "${KUBE_CONTEXT:-}" ]] && kube+=(--context "$KUBE_CONTEXT")
kube+=(-n "$NAMESPACE")

if ! "${kube[@]}" get deploy paladin-core-api >/dev/null 2>&1; then
    echo "no paladin-core-api deployment in namespace '$NAMESPACE' — is the cluster up?" >&2
    exit 1
fi

password="${ADMIN_PASSWORD:-}"
if [[ -z "$password" ]]; then
    password=$("${kube[@]}" get secret paladin-bootstrap-admin \
        -o jsonpath='{.data.password}' | base64 -d)
fi
if [[ -z "$password" ]]; then
    echo "could not read the bootstrap admin password from secret/paladin-bootstrap-admin" >&2
    exit 1
fi

# Reachability check before Playwright spends a minute failing on login. The
# console must answer; the API host answers 404 at its root by design, so only
# a refused or timed-out connection counts against it.
if ! curl -sk -o /dev/null --max-time "$REACH_TIMEOUT_SECONDS" --fail "https://${BASE_HOST}/login"; then
    echo "https://${BASE_HOST}/login does not answer — check DNS (the GitOps repo's 'task network:dns-setup') and the IngressRoute" >&2
    exit 1
fi
if ! curl -sk -o /dev/null --max-time "$REACH_TIMEOUT_SECONDS" "https://${API_HOST}/"; then
    echo "https://${API_HOST}/ is unreachable — check DNS and the IngressRoute" >&2
    exit 1
fi

admin_url="${ADMIN_URL:-}"
if [[ -z "$admin_url" ]]; then
    "${kube[@]}" port-forward svc/paladin-core-admin "${ADMIN_LOCAL_PORT}:${ADMIN_SERVICE_PORT}" >/dev/null 2>&1 &
    forward_pid=$!
    trap 'kill "$forward_pid" 2>/dev/null || true' EXIT
    admin_url="http://localhost:${ADMIN_LOCAL_PORT}"
    for _ in $(seq "$FORWARD_WAIT_SECONDS"); do
        curl -s -o /dev/null --max-time 1 "$admin_url/" && break
        sleep 1
    done
    if ! curl -s -o /dev/null --max-time 1 "$admin_url/"; then
        echo "the port-forward to svc/paladin-core-admin did not answer on ${admin_url}" >&2
        exit 1
    fi
fi

# The MCP server, reached through a forward of its own. The resource URL it
# is configured as is what an agent authorizes for, whatever host reaches it.
mcp_url="${MCP_URL:-}"
if [[ -z "$mcp_url" ]]; then
    "${kube[@]}" port-forward svc/paladin-core-mcp "${MCP_LOCAL_PORT}:${MCP_SERVICE_PORT}" >/dev/null 2>&1 &
    mcp_forward_pid=$!
    trap 'kill "${forward_pid:-}" "$mcp_forward_pid" 2>/dev/null || true' EXIT
    mcp_url="http://localhost:${MCP_LOCAL_PORT}${MCP_PATH}"
fi
mcp_resource="${MCP_RESOURCE:-$("${kube[@]}" get configmap paladin-core-config -o jsonpath='{.data.config\.yaml}' |
    yq -r '.mcp.oauth.resource_url // ""')}"

echo "── e2e against cluster ──"
echo "   ui:    https://${BASE_HOST}"
echo "   iam:   https://${API_HOST}"
echo "   data:  https://${API_HOST}"
echo "   admin: ${admin_url}"
echo "   mcp:   ${mcp_url} (resource ${mcp_resource:-unset})"
echo "   as:    ${ADMIN_SUBJECT}"

# The cluster serves an internal-CA certificate. Node's own trust store does
# not carry it, so the fixtures' Connect clients would refuse to dial; the
# browser gets ignoreHTTPSErrors from playwright.config.ts.
export NODE_TLS_REJECT_UNAUTHORIZED=0
export PALADIN_E2E_BASE_URL="https://${BASE_HOST}"
export PALADIN_E2E_IAM_URL="https://${API_HOST}"
# The cluster's ingest listens on NATS, with no webhook to post an event to:
# the ingest spec skips on an empty URL.
export PALADIN_E2E_INGEST_URL=""
export PALADIN_E2E_DATA_URL="https://${API_HOST}"
export PALADIN_E2E_ADMIN_URL="$admin_url"
export PALADIN_E2E_MCP_URL="$mcp_url"
[[ -n "$mcp_resource" ]] && export PALADIN_E2E_MCP_RESOURCE="$mcp_resource"
export PALADIN_E2E_ADMIN_SUBJECT="$ADMIN_SUBJECT"
export PALADIN_E2E_ADMIN_PASSWORD="$password"
# The cluster runs the worker, so teardown can remove a bucket from its
# backend too and wait for it (tests/e2e/fixtures/seed.ts deleteBuckets).
export PALADIN_E2E_STACK_RUNS_WORKER=1

# Playwright's own worker count unless WORKERS says otherwise. Every test signs
# in, and a cluster on the built-in login limits (auth.login_rate_limit_per_*,
# 10 a minute per subject) refuses the suite after its first few minutes of
# logins, whatever the worker count: raise both limits on the cluster before a
# full run.
workers=()
[[ -n "${WORKERS:-}" ]] && workers=(--workers="$WORKERS")

# Playwright takes its config and test directory from the working directory,
# so run it from the console's package wherever the script was started. A
# path the caller named is resolved where the caller stood first, so
# `frontend/tests/e2e/x.spec.ts` from the repository root and
# `tests/e2e/x.spec.ts` from frontend/ name the same file.
frontend_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
args=()
for arg in "$@"; do
    if [[ "$arg" != -* && -e "$arg" ]]; then
        args+=("$(cd "$(dirname "$arg")" && pwd)/$(basename "$arg")")
    else
        args+=("$arg")
    fi
done
cd "$frontend_dir"

# Not exec: the EXIT trap has to outlive Playwright to stop the forward.
npx playwright test "${workers[@]+"${workers[@]}"}" "${args[@]+"${args[@]}"}"
