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
#   - the admin plane has its own hostname (traefik.planes.admin.hosts), so
#     ADMIN_URL is NOT the same host as IAM/DATA;
#   - the bootstrap admin's password lives in a Secret, and its subject is
#     `admin`, not the compose fixture's e2e-admin@local — the weak-secret
#     deny-list refuses that one outside a disposable environment.
#
# Usage:
#   ./scripts/e2e-cluster.sh                    # whole suite
#   ./scripts/e2e-cluster.sh tests/e2e/x.spec.ts   # one file
#   NAMESPACE=other ./scripts/e2e-cluster.sh --headed
#
# NOTE: fixtures are not torn down (BACKLOG). Every run leaves tenants,
# collections and objects behind in the cluster it ran against.
set -euo pipefail

NAMESPACE="${NAMESPACE:-paladin}"
BASE_HOST="${BASE_HOST:-paladin.local}"
API_HOST="${API_HOST:-api.paladin.local}"
ADMIN_HOST="${ADMIN_HOST:-admin.paladin.local}"
ADMIN_SUBJECT="${ADMIN_SUBJECT:-admin}"

if ! kubectl -n "$NAMESPACE" get deploy paladin-core-api >/dev/null 2>&1; then
    echo "no paladin-core-api deployment in namespace '$NAMESPACE' — is the cluster up?" >&2
    exit 1
fi

password="${ADMIN_PASSWORD:-}"
if [[ -z "$password" ]]; then
    password=$(kubectl -n "$NAMESPACE" get secret paladin-bootstrap-admin \
        -o jsonpath='{.data.password}' | base64 -d)
fi
if [[ -z "$password" ]]; then
    echo "could not read the bootstrap admin password from secret/paladin-bootstrap-admin" >&2
    exit 1
fi

# Reachability check before Playwright spends a minute failing on login. A
# name that resolves but 404s means the route is missing, which is a different
# problem from the cluster being down — say which.
for host in "$BASE_HOST" "$API_HOST" "$ADMIN_HOST"; do
    if ! curl -sk -o /dev/null --max-time 8 "https://${host}/"; then
        echo "https://${host}/ is unreachable — check DNS (gitops 'task dns:setup') and the IngressRoute" >&2
        exit 1
    fi
done

echo "── e2e against cluster ──"
echo "   ui:    https://${BASE_HOST}"
echo "   iam:   https://${API_HOST}"
echo "   data:  https://${API_HOST}"
echo "   admin: https://${ADMIN_HOST}"
echo "   as:    ${ADMIN_SUBJECT}"

# The cluster serves an internal-CA certificate. Node's own trust store does
# not carry it, so the fixtures' Connect clients would refuse to dial; the
# browser gets ignoreHTTPSErrors from playwright.config.ts.
export NODE_TLS_REJECT_UNAUTHORIZED=0
export PALADIN_E2E_BASE_URL="https://${BASE_HOST}"
export PALADIN_E2E_IAM_URL="https://${API_HOST}"
export PALADIN_E2E_DATA_URL="https://${API_HOST}"
export PALADIN_E2E_ADMIN_URL="https://${ADMIN_HOST}"
export PALADIN_E2E_ADMIN_SUBJECT="$ADMIN_SUBJECT"
export PALADIN_E2E_ADMIN_PASSWORD="$password"

exec npx playwright test --workers="${WORKERS:-1}" "$@"
