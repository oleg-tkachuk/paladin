#!/usr/bin/env bash
# stack-ports.test.sh — the port defaults in scripts/stack-ports.sh must equal
# the ones the compose file interpolates.
#
# Two files have to agree about six numbers, which is the shape of every
# divergence this repository has had to chase. The failure mode if they drift is
# quiet and nasty: the preflight checks one set of ports, compose publishes
# another, and a gate boots on top of whatever was already listening.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — seconds, no Docker.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
compose="$root/frontend/tests/e2e/docker-compose.test.yaml"
lib="$root/scripts/stack-ports.sh"

# Read the defaults the LIBRARY declares, in a subshell with the environment
# cleared, so an exported override in the caller's shell cannot mask a drift.
declared=$(env -u PALADIN_E2E_PORT_DATA -u PALADIN_E2E_PORT_IAM \
    -u PALADIN_E2E_PORT_ADMIN -u PALADIN_E2E_PORT_S3 \
    -u PALADIN_E2E_PORT_UI -u PALADIN_E2E_PORT_PG \
    bash -c "source '$lib'; \
        for v in DATA IAM ADMIN S3 UI PG; do \
            eval \"echo \\\$v=\\\$PALADIN_E2E_PORT_\$v\"; \
        done" | sort)

# Read the defaults the COMPOSE FILE interpolates, straight out of its
# ${PALADIN_E2E_PORT_X:-N} expressions.
interpolated=$(grep -o '\${PALADIN_E2E_PORT_[A-Z0-9]*:-[0-9]*}' "$compose" |
    sed 's/\${PALADIN_E2E_PORT_//; s/:-/=/; s/}//' | sort -u)

if [[ "$declared" != "$interpolated" ]]; then
    {
        echo "!!! port defaults disagree between:"
        echo "      scripts/stack-ports.sh"
        echo "      frontend/tests/e2e/docker-compose.test.yaml"
        echo
        diff <(echo "$declared") <(echo "$interpolated") |
            sed 's/^</      lib:     /; s/^>/      compose: /' || true
    } >&2
    exit 1
fi

# Every port the compose file publishes must be one the library KNOWS — i.e.
# has a default for. Not "checks": which ports a given gate checks is that
# gate's business, and legitimately differs (the backend gate boots `api admin`
# and never publishes the UI, so it does not care whether :3000 is taken; the
# Playwright gate passes it as an extra). What must not happen is compose
# gaining a published port the library has never heard of, because then it is
# neither defaulted nor overridable nor checkable by anyone.
published=$(grep -o '"\${PALADIN_E2E_PORT_[A-Z0-9]*:-[0-9]*}:[0-9]*"' "$compose" |
    sed 's/.*PORT_//; s/:-.*//' | sort -u)
for name in $published; do
    if ! grep -qx "$name=[0-9]*" <<<"$declared"; then
        echo "!!! compose publishes PALADIN_E2E_PORT_$name; scripts/stack-ports.sh has no default for it" >&2
        exit 1
    fi
done

# The compose file must read the image tags from the variables the library
# exports; a renamed variable leaves the stack on `:latest` again, silently.
for var in PALADIN_CORE_TAG PALADIN_CONSOLE_TAG; do
    exported=$(bash -c "source '$lib'; echo \$STACK_${var#PALADIN_}_VAR")
    if [[ "$exported" != "$var" ]]; then
        echo "!!! scripts/stack-ports.sh exports $exported where compose reads $var" >&2
        exit 1
    fi
    if ! grep -q "\${$var:-latest}" "$compose"; then
        echo "!!! $compose does not read its image tag from \${$var:-latest}" >&2
        exit 1
    fi
done

# stack_built_version reads the stamped version and refuses a missing file or a
# file without one, rather than handing compose an empty tag.
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
printf 'APP_VERSION=1.2.3-dev.1.gabc\nGIT_COMMIT_HASH=abc\n' >"$scratch/ok.env"
printf 'GIT_COMMIT_HASH=abc\n' >"$scratch/none.env"
got=$(bash -c "source '$lib'; stack_built_version '$scratch/ok.env'")
if [[ "$got" != "1.2.3-dev.1.gabc" ]]; then
    echo "!!! stack_built_version read '$got', want 1.2.3-dev.1.gabc" >&2
    exit 1
fi
for bad in "$scratch/none.env" "$scratch/missing.env"; do
    if bash -c "source '$lib'; stack_built_version '$bad'" >/dev/null 2>&1; then
        echo "!!! stack_built_version accepted $bad" >&2
        exit 1
    fi
done

echo "stack ports agree: $(tr '\n' ' ' <<<"$declared")"
