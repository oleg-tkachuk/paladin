#!/usr/bin/env bash
# verify-stack.sh — run every gate that needs a live Paladin stack.
#
# Boots frontend/tests/e2e/docker-compose.test.yaml once, asserts the planes
# are ready, and drives four suites against it: the RPC-surface gate, the Go
# admin e2e tests, the S3 conformance suite (against the stack's MinIO) and
# dev-bootstrap.sh. Invoked as `task backend:test:stack` (which builds the
# backend image first) or `task verify-deep` (which adds the Postgres-backed
# integration suites in front).
#
# What they have in common is that none of them ran. Each was reachable only by
# typing a recipe out of its own header comment, and every one of them rotted
# for it: the e2e suite stopped compiling, the surface gate spent its life
# t.Skip-ing, the conformance suite had no endpoint to conform to, the
# readiness smoke test was filtered out of the only run that had a stack, and
# this bootstrap script was calling a proto package that had not existed for
# months. `task backend:test:tagged:compile` catches the first of those in
# seconds; it cannot catch any of the rest, because a suite that builds and
# then skips passes a compiler cleanly.
#
# Usage: verify-stack.sh [phase ...]
#   phases: rpc-surface | e2e-go | conformance | dev-bootstrap
#   (default: all four, in that order)
#
# A subset still boots the stack, runs the preflight and asserts readiness. It
# exists so `task backend:test:rpc-surface` can be one phase of this script
# rather than a second, unguarded copy of the same boot sequence — the copy it
# replaced had neither the collision check nor the image rebuild, so it quietly
# tested whatever image happened to be lying around.
#
# Requires: docker, go, jq, curl. ~5 minutes after the image build.

set -euo pipefail

ALL_PHASES=(rpc-surface e2e-go conformance dev-bootstrap)

requested=("$@")
[[ ${#requested[@]} -eq 0 ]] && requested=("${ALL_PHASES[@]}")
for want in "${requested[@]}"; do
    case " ${ALL_PHASES[*]} " in
    *" $want "*) ;;
    *)
        echo "unknown phase: $want (want one of: ${ALL_PHASES[*]})" >&2
        exit 2
        ;;
    esac
done
selected() { [[ " ${requested[*]} " == *" $1 "* ]]; }

root=$(git rev-parse --show-toplevel)
cd "$root"

project=${PALADIN_VERIFY_PROJECT:-paladin-verify-deep}
compose=(docker compose -p "$project" -f frontend/tests/e2e/docker-compose.test.yaml)

# Host ports, defaulted to the compose file's own defaults and exported so the
# compose file, the Go suites and the addresses below all read one set. Override
# them together to run this beside a Playwright stack:
#
#   PALADIN_VERIFY_PROJECT=paladin-alt PALADIN_E2E_PORT_UI=13000 \
#   PALADIN_E2E_PORT_DATA=18080 PALADIN_E2E_PORT_IAM=18085 \
#   PALADIN_E2E_PORT_ADMIN=18090 PALADIN_E2E_PORT_S3=19000 \
#   PALADIN_E2E_PORT_PG=15434  task backend:test:stack
: "${PALADIN_E2E_PORT_DATA:=8080}"
: "${PALADIN_E2E_PORT_IAM:=8085}"
: "${PALADIN_E2E_PORT_ADMIN:=8090}"
: "${PALADIN_E2E_PORT_S3:=9000}"
: "${PALADIN_E2E_PORT_UI:=3000}"
: "${PALADIN_E2E_PORT_PG:=5434}"
export PALADIN_E2E_PORT_DATA PALADIN_E2E_PORT_IAM PALADIN_E2E_PORT_ADMIN
export PALADIN_E2E_PORT_S3 PALADIN_E2E_PORT_UI PALADIN_E2E_PORT_PG

data_url="http://localhost:${PALADIN_E2E_PORT_DATA}"
iam_url="http://localhost:${PALADIN_E2E_PORT_IAM}"
admin_url="http://localhost:${PALADIN_E2E_PORT_ADMIN}"
s3_url="http://localhost:${PALADIN_E2E_PORT_S3}"

# ─── phases ──────────────────────────────────────────────────────────────────

# The only check that covers all RPC declarations at once, enumerated from the
# protobuf descriptors rather than a list someone maintains. PALADIN_RPC_SURFACE=1
# turns its t.Skip into a failure: an unreachable stack must fail the gate, not
# be excused by it.
phase_rpc_surface() {
    echo ">>> [stack] RPC surface"
    cd backend
    PALADIN_RPC_SURFACE=1 go test -tags=integration -count=1 \
        -timeout=10m -run 'Surface|RPC' ./tests/integration/...
}

# The suite skips itself when PALADIN_ADMIN_URL is unset, and a skipped suite
# prints `ok` — indistinguishable from a passing one at the exit code. The var
# is exported below so the skip cannot fire; these assertions are what prove it
# didn't, because "the gate ran and asserted nothing" is the exact state this
# whole script exists to make impossible.
phase_e2e_go() {
    echo ">>> [stack] Go admin e2e"
    local log
    log=$(mktemp -t paladin-e2e-go)
    (cd backend && go test -tags=e2e -count=1 -timeout=10m -v ./tests/e2e/...) | tee "$log"
    if grep -q -- '--- SKIP' "$log"; then
        echo "!!! the admin e2e suite SKIPPED — a skip here is a silent pass" >&2
        grep -- '--- SKIP' "$log" >&2
        return 1
    fi
    if ! grep -q -- '--- PASS' "$log"; then
        echo "!!! the admin e2e suite ran no tests" >&2
        return 1
    fi
    rm -f "$log"
}

# The conformance suite states what a storage backend must do for Paladin to
# work, and it had the same problem as everything else here: it skips without
# an endpoint, `tagged:compile` says so in as many words ("it never runs in the
# gate — which is exactly the condition under which the other two rotted"), and
# nothing supplied one. The stack already publishes MinIO on :9000 under its
# root credentials, so an endpoint costs nothing and the suite creates and
# removes its own bucket.
#
# This is MinIO conformance, not S3 conformance — the point is to notice when
# an assertion stops holding anywhere, not to certify AWS. The real-AWS profile
# is in tests/conformance/README.md and stays a deliberate, credentialed run.
phase_conformance() {
    echo ">>> [stack] S3 conformance (MinIO)"
    local log
    log=$(mktemp -t paladin-conformance)
    (cd backend && PALADIN_CONFORMANCE_ENDPOINT="$s3_url" \
        PALADIN_CONFORMANCE_PROVIDER=minio \
        PALADIN_CONFORMANCE_ACCESS_KEY=paladin-e2e-access \
        PALADIN_CONFORMANCE_SECRET_KEY=paladin-e2e-secret-key \
        PALADIN_CONFORMANCE_PATH_STYLE=true \
        go test -tags=conformance -count=1 -timeout=10m -v ./tests/conformance/...) | tee "$log"
    # Same reason as the readiness step: the suite's whole failure mode is
    # skipping quietly, so "it passed" has to mean "it asserted".
    if ! grep -q -- '--- PASS' "$log"; then
        echo "!!! the conformance suite asserted nothing" >&2
        return 1
    fi
    rm -f "$log"
}

# Run TWICE, and both runs must pass. Once proves it works against an empty
# database; twice proves the idempotency keys do what the script claims — every
# Create* here is rejected outright without an Idempotency-Key, and the keys are
# derived from resource identity so a second run replays rather than colliding.
# A single run cannot tell a replay from `ensure` quietly forgiving an
# already_exists.
phase_dev_bootstrap() {
    echo ">>> [stack] dev-bootstrap.sh (twice — the second run proves idempotency)"
    # The token must be BOUND to the tenant the script provisions, not merely
    # platform-admin. A platform-only token (no `tenant` claim) gets through
    # CreateTenant and CreateBucket — both cross-tenant admin writes — and then
    # fails on CreateCollection with "principal is not bound to a tenant",
    # because a collection is created by a member of the tenant that owns it.
    # That is the shape of the dev token the script documents; minting a rootier
    # one is not a stronger test, just a differently broken one.
    #
    # The tenant does not exist at mint time and does not need to: the claim is
    # read from the token, and step 1 of the script is what creates the row.
    local dev_tenant=3a823fd4-0b3d-4ce2-a280-93b8d75cc07b
    JWT=$(JWT_SECRET="$PALADIN_JWT_SECRET" JWT_ISS="$PALADIN_JWT_ISSUER" \
        JWT_AUD=paladin-admin JWT_ROLES=platform.admin JWT_SUB=verify-deep \
        JWT_TENANT="$dev_tenant" JWT_TENANT_SLUG=ui-dev \
        bash backend/scripts/auth-mint-jwt.sh)
    export JWT
    export TENANT_ID="$dev_tenant"
    # BUCKET_ID matches the bucket compose actually created in MinIO
    # (PALADIN_STORAGE_BACKENDS_PRIMARY_BUCKET), not the script's cluster-shaped
    # default — otherwise it registers a bucket row for a bucket that is not
    # there.
    export PALADIN_HOST="$data_url"
    export PALADIN_ADMIN_HOST="$admin_url"
    export BUCKET_ID=paladin-e2e
    bash frontend/scripts/dev-bootstrap.sh
    bash frontend/scripts/dev-bootstrap.sh
}

# ─── the stack ───────────────────────────────────────────────────────────────

cleanup() { "${compose[@]}" down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT
"${compose[@]}" down -v >/dev/null 2>&1 || true

# Preflight: are the ports we are about to publish free?
#
# Ports are now the only thing that can collide. The compose file used to pin a
# `container_name:` on every service too, which made the check "is any
# paladin-e2e-* container running" and made a second stack impossible at any port
# — the names are gone, so this asks the question that is actually left.
#
# Still refuse rather than reclaim, and for the same reason: whatever holds the
# port is usually a developer's own stack — the run that first hit this had one
# 22 hours old — and a gate that destroys the environment it was invoked from is
# worse than one that declines to start. The difference is that "use other
# ports" is now a real answer, so the message says how.
busy=""
for port in "$PALADIN_E2E_PORT_DATA" "$PALADIN_E2E_PORT_IAM" \
    "$PALADIN_E2E_PORT_ADMIN" "$PALADIN_E2E_PORT_S3" "$PALADIN_E2E_PORT_PG"; do
    if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
        busy+="      :$port — $(lsof -nP -iTCP:"$port" -sTCP:LISTEN -Fc 2>/dev/null |
            sed -n 's/^c//p' | sort -u | tr '\n' ' ')"$'\n'
    fi
done
if [[ -n "$busy" ]]; then
    {
        echo "!!! ports this stack publishes are already in use:"
        printf '%s' "$busy"
        echo
        echo "    Free them, or give this run its own set — the compose file"
        echo "    takes an override for every published port:"
        echo
        echo "      PALADIN_VERIFY_PROJECT=paladin-alt PALADIN_E2E_PORT_DATA=18080 \\"
        echo "        PALADIN_E2E_PORT_IAM=18085 PALADIN_E2E_PORT_ADMIN=18090 \\"
        echo "        PALADIN_E2E_PORT_S3=19000 PALADIN_E2E_PORT_PG=15434 \\"
        echo "        task backend:test:stack"
    } >&2
    exit 1
fi

# `api admin` rather than the whole file. The console image is the Playwright
# suite's dependency; none of these gates opens a browser, and requiring a
# frontend build to run backend gates is how a gate acquires a reason to be
# skipped. compose still pulls in postgres, migrate, promote-app-role,
# bootstrap and minio as declared dependencies.
echo ">>> [stack] booting api + admin"
"${compose[@]}" up --wait api admin

# Matches backend/configs/compose.yaml (auth.signing_key / auth.issuer).
export PALADIN_JWT_SECRET=dev-secret-change-me-32-bytes-min
export PALADIN_JWT_ISSUER=paladin-dev
export PALADIN_ADMIN_URL="$admin_url"
# Three suites, three namings for the same three addresses: the Playwright
# fixtures and the smoke test read PALADIN_E2E_*_URL, the RPC-surface gate reads
# PALADIN_RPC_*_URL, and the Go admin e2e suite reads PALADIN_ADMIN_URL. Export
# all of them from the one port block, because the alternative is what actually
# happened on the first alternate-port run: the surface gate kept its
# 127.0.0.1:8090 default, reached a kubectl port-forward to the dev CLUSTER, and
# reported 60 seconds of failures about a deployment it was never meant to
# touch. Unifying the names is a BACKLOG item; covering all three is the fix
# that makes this script honest today.
export PALADIN_E2E_DATA_URL="$data_url"
export PALADIN_E2E_IAM_URL="$iam_url"
export PALADIN_E2E_ADMIN_URL="$admin_url"
export PALADIN_RPC_DATA_URL="$data_url"
export PALADIN_RPC_IAM_URL="$iam_url"
export PALADIN_RPC_ADMIN_URL="$admin_url"

# Readiness, asserted rather than assumed — and the one place TestSmokeStackReady
# runs at all.
#
# It belongs to the boot, not to a phase: it is the claim every phase below
# depends on ("all three planes answer /readyz"), so a subset run gets it too,
# and a failure here names the plane instead of surfacing as a mystery inside
# whichever suite happened to go first.
#
# PALADIN_SMOKE=1 is what makes it a test. Without it the test probes the data
# plane,
# finds nothing and skips — correct for a bare `go test -tags=integration`,
# which is why it is written that way. But the phase below filters on
# `-run 'Surface|RPC'`, which does not match `TestSmokeStackReady`, so between
# the two halves of verify-deep this test executed nowhere: it skipped in the
# stackless half and was filtered out of the half that had a stack. A test that
# runs in neither is exactly what this script was built to make impossible, and
# it was one of its own blind spots.
#
# The PASS assertion is not belt-and-braces: `-run` that matches nothing prints
# a warning and exits 0, so a rename would turn this step back into the silence
# it was written to end.
echo ">>> [stack] readiness (data / iam / admin)"
smoke_log=$(mktemp -t paladin-smoke)
(cd backend && PALADIN_SMOKE=1 go test -tags=integration -count=1 \
    -timeout=2m -v -run TestSmokeStackReady ./tests/integration/...) | tee "$smoke_log"
if ! grep -q -- '--- PASS: TestSmokeStackReady' "$smoke_log"; then
    echo "!!! TestSmokeStackReady did not run — has it been renamed?" >&2
    exit 1
fi
rm -f "$smoke_log"

# Iterate ALL_PHASES, not the request, so the order is the script's and not the
# caller's argument order. Each phase runs in a subshell: phase_rpc_surface cd's
# into backend/ and the next phase must not inherit that.
for phase in "${ALL_PHASES[@]}"; do
    selected "$phase" || continue
    ("phase_${phase//-/_}")
done

echo ">>> [stack] passed: ${requested[*]}"
