#!/usr/bin/env bash
# verify-e2e.sh — run the Playwright suite against a stack built from this
# branch. Invoked as `task verify-e2e`, which builds both images first.
#
# The suite has always been runnable (`pnpm run test:e2e`), and that is exactly
# the problem this closes. Playwright's webServer brings the compose stack up
# but rebuilds nothing, and the stack runs `:latest` — so the result describes
# whichever images happen to be lying around. That is not hypothetical: on
# 2026-08-30 a "108 passed" run was read as verifying a commit whose console
# image had been built four minutes BEFORE it, and on 2026-09-01
# `paladin-console:latest` was two days and three commits stale while a newer
# console image sat beside it under a version tag. A green run against the
# wrong image is worse than no run, because it is believed.
#
# So the task builds both images and this script refuses to start if the ports
# are taken — the same discipline `task verify-deep` got, sharing the same
# preflight so the two cannot drift apart.
#
# Requires: docker, pnpm, a Chromium Playwright has installed. ~2 minutes after
# the image builds.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

# shellcheck source=SCRIPTDIR/../../scripts/stack-ports.sh
source "$root/scripts/stack-ports.sh"

# Playwright's webServer BOOTS the stack but does not remove it. Its command is
# `docker compose up --wait`, which exits as soon as the stack is healthy, so
# when the run ends there is no process left for Playwright to kill and the
# containers simply stay. Combined with `reuseExistingServer: !CI` — false
# here — that leaves the next run of this gate tripping its own preflight on the
# stack the previous one left behind. Observed exactly that.
#
# So the teardown is this script's job, and it runs BEFORE the preflight as well
# as after: reclaiming our own project is not the same as reclaiming someone
# else's, and the preflight below still refuses anything that is not ours.
# `down -v` because the Postgres volume is a tmpfs fixture — a suite that
# inherits state from the previous run passes for the wrong reason, which is the
# compose file's own stated reason for the tmpfs.
compose_project=${PALADIN_E2E_PROJECT:-paladin-e2e}
# ABSOLUTE path, deliberately. The script cd's into frontend/ before invoking
# Playwright, so by the time the EXIT trap fires a relative
# `frontend/tests/e2e/...` no longer resolves — `docker compose` errors, `|| true`
# swallows it, and the stack survives looking like the teardown ran. That is not
# a hypothetical: the first version did exactly this, and only a back-to-back
# double run surfaced it, because the next run's own pre-emptive cleanup hid the
# leak.
compose_file="$root/frontend/tests/e2e/docker-compose.test.yaml"
cleanup() {
    docker compose -p "$compose_project" -f "$compose_file" down -v >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup

# The UI port matters here and does not for the backend gate: this is the only
# gate that opens a browser, so :3000 (or its override) has to be ours. It is
# passed as an extra rather than added to the shared list for that reason.
require_free_ports "task verify-e2e" "$PALADIN_E2E_PORT_UI" || exit 1

# This script deliberately does not set PALADIN_E2E_BASE_URL. That variable is
# the "an external stack is already running" switch: playwright.config.ts turns
# the webServer block OFF when it is set. Exporting it to carry a port override
# — which is what the first version of this script did — silently skipped the
# boot and left the suite talking to nothing (`ConnectError: [unavailable]` out
# of the environment setup). The config follows PALADIN_E2E_PORT_UI on its own;
# this script only has to pass the port through, which sourcing already did.
stack_export_urls

echo ">>> [e2e] images under test"
for image in registry.local/paladin/paladin-core:latest \
    registry.local/paladin/paladin-console:latest; do
    # Printed, not merely built, because "which image did this run actually
    # test" is the question every stale-image incident here turned on. The
    # revision label is stamped from the build's git commit.
    rev=$(docker image inspect "$image" \
        --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' 2>/dev/null || true)
    created=$(docker image inspect "$image" --format '{{.Created}}' 2>/dev/null || true)
    echo "      $image  rev=${rev:-<none>}  built=${created:-<missing>}"
done
# --dirty, because the images are built from the WORKING TREE and the label is
# stamped from HEAD. Those differ whenever anything is uncommitted, and this
# line exists precisely so nobody reads a run as being about a commit it was
# not — a gap it had itself until an uncommitted fix was verified under a label
# naming the commit that contained the bug.
echo "      HEAD                                     rev=$(git describe --always --dirty --abbrev=8)"

# Run with the repository's own gate settings rather than its exploratory ones.
# playwright.config.ts keys both off CI: `workers: CI ? 2 : undefined` and
# `retries: CI ? 1 : 0`, with the local defaults chosen so flake SURFACES
# instead of being masked. That is the right stance for someone poking at a
# spec and the wrong one for a verdict: on a machine at load average 15 this
# suite produced three timeout failures across three unrelated specs, and all
# fourteen tests in those files passed in twelve seconds when re-run alone. A
# gate that fails for reasons the branch did not cause gets ignored, which is
# how it stops being a gate.
#
# Nothing is hidden by this. A test that only passes on the retry is reported
# as `flaky`, not `passed`, and the check below refuses to let that scroll by.
# Setting CI here also aligns the local gate with .github/workflows/e2e.yml
# exactly — same workers, same retries, same no-reuse of a running stack.
export CI=true

echo ">>> [e2e] Playwright"
log=$(mktemp -t paladin-e2e-pw)
cd frontend
pnpm run test:e2e 2>&1 | tee "$log"

if grep -qE '^ +[0-9]+ flaky' "$log"; then
    echo
    {
        echo ">>> [e2e] PASSED WITH FLAKE — these needed a retry:"
        # From the "N flaky" heading to the "N passed" summary: Playwright lists
        # the offending specs in between, and the spec NAME is the whole point —
        # "1 flaky" on its own tells nobody what to look at.
        awk '/^ +[0-9]+ flaky/{f=1} f{print} f&&/^ +[0-9]+ passed/{exit}' "$log"
        echo "    Re-run those alone before believing either verdict. Under load"
        echo "    this suite times out on specs that are not broken: at load"
        echo "    average 15 it failed three unrelated specs that then passed"
        echo "    14/14 in twelve seconds on their own."
    } >&2
fi
rm -f "$log"

echo ">>> [e2e] passed"
