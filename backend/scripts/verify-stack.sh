#!/usr/bin/env bash
# verify-stack.sh — run every gate that needs a live Paladin stack.
#
# Boots frontend/tests/e2e/docker-compose.test.yaml once and drives three
# suites against it: the RPC-surface gate, the Go admin e2e tests, and
# dev-bootstrap.sh. Invoked as `task backend:test:stack` (which builds the
# backend image first) or `task verify-deep` (which adds the Postgres-backed
# integration suites in front).
#
# Why these three live together: each one only ever ran by hand, from a recipe
# in its own header comment, so none of them ran — and all three rotted. The
# e2e suite stopped compiling, the surface gate spent its life t.Skip-ing, and
# this bootstrap script was calling a proto package that had not existed for
# months. `task backend:test:tagged:compile` catches the first failure mode in
# seconds; it cannot catch the other two, because a suite that builds and then
# skips passes a compiler cleanly.
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

ALL_PHASES=(rpc-surface e2e-go dev-bootstrap)

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

# Iterate ALL_PHASES, not the request, so the order is the script's and not the
# caller's argument order. Each phase runs in a subshell: phase_rpc_surface cd's
# into backend/ and the next phase must not inherit that.
for phase in "${ALL_PHASES[@]}"; do
    selected "$phase" || continue
    ("phase_${phase//-/_}")
done

echo ">>> [stack] passed: ${requested[*]}"
