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
#   phases: rpc-surface | e2e-go | dev-bootstrap   (default: all three)
#
# A subset still boots the stack and still runs the preflight. It exists so
# `task backend:test:rpc-surface` can be one phase of this script rather than a
# second, unguarded copy of the same boot sequence — the copy it replaced had
# neither the collision check nor the image rebuild, so it quietly tested
# whatever image happened to be lying around.
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

project=paladin-verify-deep
compose=(docker compose -p "$project" -f frontend/tests/e2e/docker-compose.test.yaml)

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
    (cd backend && PALADIN_CONFORMANCE_ENDPOINT=http://localhost:9000 \
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
    export PALADIN_HOST=http://localhost:8080
    export PALADIN_ADMIN_HOST=http://localhost:8090
    export BUCKET_ID=paladin-e2e
    bash frontend/scripts/dev-bootstrap.sh
    bash frontend/scripts/dev-bootstrap.sh
}

# ─── the stack ───────────────────────────────────────────────────────────────

cleanup() { "${compose[@]}" down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT
"${compose[@]}" down -v >/dev/null 2>&1 || true

# Preflight: one stack per host.
#
# This stack does not isolate by project name, and pretending it does costs a
# full image build before the collision surfaces. Every service carries a fixed
# `container_name:` (paladin-e2e-*) and every plane publishes a fixed host port
# (8080 / 8085 / 8090), so a second `-p` gets its own network and volumes and
# then fails on the first name Docker already holds.
#
# So refuse rather than reclaim. The stack occupying the slot is usually a
# developer's own — the run that found this had one 22 hours old — and a gate
# that silently destroys the environment it was invoked from is worse than a
# gate that declines to start.
squatters=$(docker ps -a --filter 'name=^paladin-e2e-' \
    --format '{{.Names}} {{.Label "com.docker.compose.project"}}' |
    awk -v p="$project" '$2 != p {print}')
if [[ -n "$squatters" ]]; then
    owner=$(echo "$squatters" | awk '{print $2; exit}')
    {
        echo "!!! the compose stack is already running under another project:"
        echo "$squatters" | sed 's/^/      /'
        echo
        echo "    This compose file pins container_name and host ports, so only"
        echo "    one instance can exist on a host. Free the slot and re-run:"
        echo
        echo "      docker compose -p $owner -f frontend/tests/e2e/docker-compose.test.yaml down -v"
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
export PALADIN_ADMIN_URL=http://localhost:8090

# Readiness, asserted rather than assumed — and the one place TestSmokeStackReady
# runs at all.
#
# It belongs to the boot, not to a phase: it is the claim every phase below
# depends on ("all three planes answer /readyz"), so a subset run gets it too,
# and a failure here names the plane instead of surfacing as a mystery inside
# whichever suite happened to go first.
#
# PALADIN_SMOKE=1 is what makes it a test. Without it the test probes :8080,
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
