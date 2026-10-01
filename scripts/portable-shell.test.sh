#!/usr/bin/env bash
# portable-shell.test.sh — the gate scripts run on macOS and on Linux CI
# runners, so they use no form only one of the two accepts.
#
# Each pattern below cost a red CI run that a Mac had passed: `df -g` (BSD df
# only) ended verify-e2e silently under pipefail, and `mktemp -t name` (BSD
# appends the random part; GNU wants XXX in the template) failed it again one
# step later.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no network.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

# pattern|why — extended regexes over tracked shell scripts.
readonly RULES=(
    'df -g|BSD df only; use `df -Pk`'
    'mktemp -t [^X"]*$|mktemp -t ([^ X)"]+)\)|GNU mktemp needs XXX in the template; use mktemp "${TMPDIR:-/tmp}/name.XXXXXX"'
)

failed=0
scripts=$(git ls-files '*.sh' | grep -v '^\.task/')
for rule in "${RULES[@]}"; do
    pattern="${rule%|*}"
    why="${rule##*|}"
    # Comment lines may name a pattern to explain why it is not used.
    hits=$(printf '%s\n' "$scripts" | xargs grep -nE "$pattern" | grep -vE '^[^:]+:[0-9]+:\s*#' || true)
    if [ -n "$hits" ]; then
        printf '!!! %s\n%s\n' "$why" "$hits" >&2
        failed=1
    fi
done

[ "$failed" -eq 0 ] || exit 1
printf '%s\n' "portable shell: no BSD-only forms in the tracked scripts"
