#!/usr/bin/env bash
# shellcheck disable=SC2034  # a sourced library: its variables are the callers'.
# stack-ports.sh — the e2e compose stack's host ports, and the check that they
# are free. SOURCE this; it is not executable on its own.
#
#   source "$(git rev-parse --show-toplevel)/scripts/stack-ports.sh"
#   require_free_ports "task -t Taskfile.dev.yaml verify-deep"
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
# already feed; the other two are gone.
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
# The list is DERIVED from the compose file, not written out here. It used to
# be written out, under a comment saying the compose file is the only thing
# that decides which services carry a remote image — and then the storage
# service was renamed and this still said `minio`, so the pull failed with
# "no such service" and the gate died ten minutes in, after both image builds.
# The message it printed offered a rate limit and a bad tag; the cause was
# neither.
#
# The criterion is the image prefix: services running an image this repository
# BUILDS cannot be pulled, and everything else must be. api/admin/ui/migrate/
# bootstrap are the former and drop out on their own. The compose file names
# that prefix through ${PALADIN_IMAGE_PREFIX:-…}, so it is matched as written.
readonly STACK_IMAGE_PREFIX_VAR=PALADIN_IMAGE_PREFIX
readonly STACK_BUILT_IMAGE_REF="\${${STACK_IMAGE_PREFIX_VAR}"

# The repository the built images are in. The task that builds them passes
# its own GLOBAL_REGISTRY/IMAGE_NAMESPACE here, so the stack runs whatever that
# build produced; the default is what Taskfile.dev.yaml builds. Exported: the
# compose file reads the same variable.
readonly STACK_DEFAULT_IMAGE_PREFIX=registry.local/paladin
: "${PALADIN_IMAGE_PREFIX:=$STACK_DEFAULT_IMAGE_PREFIX}"
export PALADIN_IMAGE_PREFIX

# stack_thirdparty_services <compose file> — the services that pull a remote
# image, one per line.
stack_thirdparty_services() {
    # yq warns on stderr about merge-anchor semantics; the anchor-using
    # services are all locally built, so it cannot change this answer.
    yq -r ".services | to_entries | .[]
           | select((.value.image // \"\")
               | contains(\"${STACK_BUILT_IMAGE_REF}\") | not)
           | .key" "${1:?compose file}" 2>/dev/null
}

stack_pull_thirdparty() {
    local compose_file="${1:?compose file}"
    local services=()
    while IFS= read -r svc; do
        [ -n "$svc" ] && services+=("$svc")
    done < <(stack_thirdparty_services "$compose_file")

    # An empty list would make this a no-op that reports success, which is
    # exactly the failure it exists to prevent.
    if [ ${#services[@]} -eq 0 ]; then
        echo "!!! no third-party services found in $compose_file — nothing was pulled" >&2
        echo "    Either the file moved, or every service now runs a built image." >&2
        return 1
    fi

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

# ─── which images the stack runs ─────────────────────────────────────────────
# The compose file runs the two images this repository builds by
# ${PALADIN_CORE_TAG:-latest} and ${PALADIN_CONSOLE_TAG:-latest}. A gate exports
# the version its own image build just stamped, so the stack runs that image and
# not whatever `:latest` is lying around. The image build tags the version only
# — `:latest` is a push-time option of the release library — so a gate that
# relied on `:latest` failed outright the day no stale one was left to find.
readonly STACK_CORE_TAG_VAR=PALADIN_CORE_TAG
readonly STACK_CONSOLE_TAG_VAR=PALADIN_CONSOLE_TAG
# The build writes APP_VERSION here (release library, `version` step).
readonly STACK_CORE_INFO=backend/deploy/info.env
readonly STACK_CONSOLE_INFO=frontend/deploy/info.env
readonly STACK_VERSION_KEY=APP_VERSION

# stack_built_version <info.env> — the version the last image build stamped.
stack_built_version() {
    local info="${1:?info.env}" version
    if [ ! -f "$info" ]; then
        echo "!!! $info not found — build the image first (task <component>:release:image:build)" >&2
        return 1
    fi
    version=$(sed -n "s/^${STACK_VERSION_KEY}=//p" "$info")
    if [ -z "$version" ]; then
        echo "!!! $info carries no ${STACK_VERSION_KEY}" >&2
        return 1
    fi
    printf '%s\n' "$version"
}

# stack_use_built_image <tag variable> <info.env> <image> — export the built
# version into the variable the compose file reads, after checking the image
# under that tag is in the local store.
stack_use_built_image() {
    local var="${1:?tag variable}" info="${2:?info.env}" image="${3:?image}" version
    version=$(stack_built_version "$info") || return 1
    if ! docker image inspect "$image:$version" >/dev/null 2>&1; then
        {
            echo "!!! $image:$version is not in the local image store."
            echo "    $info names that version, so the build ran — into another"
            echo "    repository than $STACK_IMAGE_PREFIX_VAR=$PALADIN_IMAGE_PREFIX. The task"
            echo "    that builds the image passes its own; run the gate through it."
        } >&2
        return 1
    fi
    export "$var=$version"
}
