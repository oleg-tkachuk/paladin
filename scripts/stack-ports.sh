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
: "${PALADIN_E2E_PORT_DATA:=8080}"
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

# stack_export_urls — publish the plane addresses under every name a suite in
# this repository reads.
#
# Three namings exist for the same three addresses: PALADIN_E2E_*_URL (Playwright
# fixtures, the Go smoke test), PALADIN_RPC_*_URL (the RPC-surface gate) and a
# bare PALADIN_ADMIN_URL (the Go admin e2e suite). Setting one set leaves the
# others on their defaults, which is not a hypothetical: the first
# alternate-port run exported only the first set, and the surface gate kept its
# 127.0.0.1:8090 default — a kubectl port-forward to the dev CLUSTER on that
# machine — then spent a minute reporting authz failures against a deployment it
# was never pointed at. Unifying the names is a BACKLOG item; until then, one
# function sets all of them.
stack_export_urls() {
    export PALADIN_E2E_DATA_URL="$stack_data_url"
    export PALADIN_E2E_IAM_URL="$stack_iam_url"
    export PALADIN_E2E_ADMIN_URL="$stack_admin_url"
    export PALADIN_RPC_DATA_URL="$stack_data_url"
    export PALADIN_RPC_IAM_URL="$stack_iam_url"
    export PALADIN_RPC_ADMIN_URL="$stack_admin_url"
    export PALADIN_ADMIN_URL="$stack_admin_url"
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
