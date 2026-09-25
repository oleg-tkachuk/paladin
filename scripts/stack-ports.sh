#!/usr/bin/env bash
# shellcheck disable=SC2034  # a sourced library: its variables are the callers'.
# stack-ports.sh — the e2e compose stack's host ports, and the check that they
# are free. SOURCE this; it is not executable on its own.
#
#   source "$(git rev-parse --show-toplevel)/scripts/stack-ports.sh"
#   require_free_ports "task verify-deep"
#
# Why it is shared rather than copied: two gates now boot the same
# frontend/tests/e2e/docker-compose.test.yaml — backend/scripts/verify-stack.sh
# and frontend/scripts/verify-e2e.sh — and a second copy of "which ports does
# the stack publish" is a list that drifts from the compose file in one place
# and not the other. The defaults below MUST match the compose file's own
# `${PALADIN_E2E_PORT_*:-…}` defaults; there is one test asserting exactly that
# (scripts/stack-ports.test.sh), because agreement by inspection is how the
# other divergences in this repository started.

# Defaults mirror frontend/tests/e2e/docker-compose.test.yaml. Exported so the
# compose file interpolates the same values the caller resolved.
: "${PALADIN_E2E_PORT_DATA:=8083}"
: "${PALADIN_E2E_PORT_IAM:=8085}"
: "${PALADIN_E2E_PORT_ADMIN:=8090}"
: "${PALADIN_E2E_PORT_S3:=9000}"
: "${PALADIN_E2E_PORT_UI:=3000}"
: "${PALADIN_E2E_PORT_PG:=5434}"
export PALADIN_E2E_PORT_DATA PALADIN_E2E_PORT_IAM PALADIN_E2E_PORT_ADMIN
export PALADIN_E2E_PORT_S3 PALADIN_E2E_PORT_UI PALADIN_E2E_PORT_PG

stack_data_url="http://localhost:${PALADIN_E2E_PORT_DATA}"
stack_iam_url="http://localhost:${PALADIN_E2E_PORT_IAM}"
stack_admin_url="http://localhost:${PALADIN_E2E_PORT_ADMIN}"
stack_s3_url="http://localhost:${PALADIN_E2E_PORT_S3}"
stack_ui_url="http://localhost:${PALADIN_E2E_PORT_UI}"

# stack_export_urls — publish the plane addresses under the one name every
# suite reads.
#
# There used to be three names for the same three addresses: PALADIN_E2E_*_URL
# (Playwright fixtures, the smoke test), PALADIN_RPC_*_URL (the RPC-surface gate)
# and a bare PALADIN_ADMIN_URL (the Go admin e2e suite). Setting one set left the
# others on their defaults, and that cost a real run: exporting only the first
# left the surface gate on 127.0.0.1:8090, which on that machine was a kubectl
# port-forward to the dev CLUSTER, so it spent a minute reporting authz failures
# about a deployment it was never pointed at. A suite silently testing the wrong
# target is the same species of bug as a suite silently skipping.
#
# PALADIN_E2E_*_URL won because it is the set the compose file's port overrides
# already feed. The other two survive as deprecated aliases inside the suites
# that used them (see the comments there) for one release; this function
# deliberately does NOT export them, so if a suite still depends on an alias the
# gate fails and names it rather than working by accident.
stack_export_urls() {
    export PALADIN_E2E_DATA_URL="$stack_data_url"
    export PALADIN_E2E_IAM_URL="$stack_iam_url"
    export PALADIN_E2E_ADMIN_URL="$stack_admin_url"
}

# require_free_ports <re-run command> [extra port ...]
#
# Refuses rather than reclaims. Whatever holds a port is usually a developer's
# own stack — the run that first hit this had one 22 hours old — and a gate that
# destroys the environment it was invoked from is worse than one that declines
# to start. Since every published port is an override, "use another set" is a
# real answer, so the message says how.
require_free_ports() {
    local rerun=$1
    shift
    local ports=("$PALADIN_E2E_PORT_DATA" "$PALADIN_E2E_PORT_IAM"
        "$PALADIN_E2E_PORT_ADMIN" "$PALADIN_E2E_PORT_S3" "$PALADIN_E2E_PORT_PG" "$@")
    local busy="" port holders owner
    for port in "${ports[@]}"; do
        if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
            holders=$(lsof -nP -iTCP:"$port" -sTCP:LISTEN -Fc 2>/dev/null |
                sed -n 's/^c//p' | sort -u | tr '\n' ' ')
            # When Docker is the listener the process name is its proxy
            # ("OrbStack Helper", "com.docker.backend") and says nothing about
            # what to stop. Ask Docker which compose project publishes the port
            # instead — that is the name the operator needs, and usually a
            # forgotten stack of their own.
            owner=$(docker ps --format '{{.Label "com.docker.compose.project"}} {{.Ports}}' 2>/dev/null |
                awk -v p=":$port->" '$0 ~ p {print $1; exit}')
            [[ -n "$owner" ]] && holders+="(compose project: $owner) "
            busy+="      :$port — $holders"$'\n'
        fi
    done
    [[ -z "$busy" ]] && return 0
    {
        echo "!!! ports this stack publishes are already in use:"
        printf '%s' "$busy"
        echo
        echo "    A compose project named above comes down with:"
        echo "      docker compose -p <project> \\"
        echo "        -f frontend/tests/e2e/docker-compose.test.yaml down -v"
        echo
        echo "    Or give this run its own set — the compose file takes an"
        echo "    override for every published port:"
        echo
        echo "      PALADIN_E2E_PORT_UI=13000 PALADIN_E2E_PORT_DATA=18080 \\"
        echo "        PALADIN_E2E_PORT_IAM=18085 PALADIN_E2E_PORT_ADMIN=18090 \\"
        echo "        PALADIN_E2E_PORT_S3=19000 PALADIN_E2E_PORT_PG=15434 \\"
        echo "        $rerun"
    } >&2
    return 1
}

# ─── third-party images ──────────────────────────────────────────────────────

# Pull the Docker Hub images the stack needs, BEFORE `compose up` reaches for
# them.
#
# `compose up` pulls what is missing on its own, so this is not about fetching —
# it is about what happens when the fetch fails. On 2026-09-04 a verify-deep run
# pulled postgres:16 and minio/mc, silently did not pull minio/minio, and then
# reported:
#
#     Error response from daemon: No such image: minio/minio:RELEASE.…
#
# which reads as "this image does not exist" and sends the next reader looking
# for a bad tag or a corrupted store. Both were fine; `docker pull` succeeded by
# hand on the first try. What actually failed was a concurrent pull, and nothing
# in the message said so — Docker Hub rate limits and concurrent pulls are a
# known hazard, here wearing the wrong name.
#
# Pulling first means a fetch failure is reported as a fetch failure, once,
# before three phases of stack boot are stacked on top of it.
#
# The service list is here rather than in the two callers for the reason the
# ports are: two copies of it would drift, and the compose file is the only
# thing that decides which services carry a remote image. api/admin/ui are
# deliberately absent — they run registry.local images this repo builds, and
# `pull` on those fails.
stack_pull_thirdparty() {
    local compose_file="${1:?compose file}"
    local services=(minio minio-setup postgres promote-app-role)
    echo ">>> [stack] pulling third-party images: ${services[*]}"
    if docker compose -f "$compose_file" pull --quiet "${services[@]}"; then
        return 0
    fi
    {
        echo "!!! could not pull the third-party images above."
        echo
        echo "    Whatever docker said, it said it about a FETCH — not about a"
        echo "    stack that is missing an image. Two things it can be:"
        echo
        echo "      - the registry refused or timed out. Docker Hub"
        echo "        rate-limits anonymous pulls; postgres comes from there."
        echo "        Retry, or:  docker login"
        echo "      - a tag in the compose file is wrong. Then the message above"
        echo "        names it, and no retry will help."
    } >&2
    return 1
}
