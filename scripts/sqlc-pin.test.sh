#!/usr/bin/env bash
# sqlc-pin.test.sh — the sqlc drift gate must regenerate with the version
# go.mod pins, never with whatever `sqlc` happens to be on PATH.
#
# sqlc stamps its own version into a banner comment at the top of every file it
# writes. So a PATH sqlc one minor away from the pin rewrites all of
# internal/store/postgres/sqlc with nothing but that line changed, and the gate
# then reports the output as stale — blaming the developer for drift the gate
# itself had just created, and leaving the rewritten files behind in the tree.
# Observed on 2026-09-18 with PATH sqlc v1.31.1 against v1.30.0-generated
# output: 29 files, 54 insertions, 54 deletions, every hunk the banner.
#
# The fix is SQLC_MODULE in backend/Taskfile.yaml, which makes the library run
# sqlc through `go run`. This test proves it by putting a sqlc on PATH that
# fails loudly if anything calls it: the gate must still pass.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — needs the Go toolchain,
# no Docker.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
sqlc_out="backend/internal/store/postgres/sqlc"

# `go run` is the whole point of the fix, so a checkout without it cannot
# answer the question. Say so rather than passing — a gate that reports success
# when it could not run is the bug this file exists to prevent.
if ! command -v go >/dev/null 2>&1; then
    echo "!!! sqlc-pin.test.sh needs the Go toolchain to check the pin" >&2
    exit 1
fi

# The gate regenerates in place, so refuse to run over uncommitted bindings:
# the cleanup below restores them from the index and would discard the work.
if ! git -C "$root" diff --quiet -- "$sqlc_out" ||
    ! git -C "$root" diff --cached --quiet -- "$sqlc_out"; then
    echo "!!! $sqlc_out has uncommitted changes; commit or stash them first" >&2
    exit 1
fi

stub=$(mktemp -d)
# Own TMPDIR, as the lefthook jobs do: Task shares one clone directory per
# remote include across every process on the machine, with no lock.
d=$(mktemp -d)
# Restore whatever the gate regenerated. It should be a no-op when the pin
# holds; it is what keeps a failure from leaving the tree rewritten.
trap 'rm -rf "$stub" "$d"; git -C "$root" checkout --quiet -- "$sqlc_out"' EXIT

cat >"$stub/sqlc" <<'STUB'
#!/bin/sh
echo "PATH sqlc was invoked — the drift gate is not using the go.mod-pinned one" >&2
exit 42
STUB
chmod +x "$stub/sqlc"

# The stub's message is how a PATH sqlc shows itself; any other failure is the
# drift gate doing its job — stale output — and is reported as that, not
# blamed on the pin.
readonly STUB_MARK="PATH sqlc was invoked"
if ! out=$(cd "$root/backend" && PATH="$stub:$PATH" TMPDIR="$d/" TASK_OFFLINE=1 \
    task codegen:sqlc:check 2>&1); then
    if grep -qF "$STUB_MARK" <<<"$out"; then
        {
            echo "!!! the sqlc drift gate did not use the go.mod-pinned sqlc"
            echo "    set SQLC_MODULE on the codegen include in backend/Taskfile.yaml"
        } >&2
    else
        {
            echo "!!! the sqlc output is stale against the migrations and queries"
            echo "    run: (cd backend && task codegen:sqlc), and commit the result"
        } >&2
    fi
    printf '      %s\n' "${out//$'\n'/$'\n'      }" >&2
    exit 1
fi

echo "sqlc pin holds: the drift gate ignored a sabotaged PATH sqlc"
