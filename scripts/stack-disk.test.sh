#!/usr/bin/env bash
# stack-disk.test.sh — scripts/stack-disk.sh reads free space on both systems
# the gates run on.
#
# The check it backs used `df -g`, which only BSD df has; on a Linux CI runner
# it ended verify-e2e with exit 1 before printing anything.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no Docker.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
# shellcheck source=SCRIPTDIR/stack-disk.sh
source "$root/scripts/stack-disk.sh"
failed=0

check() {
    local name=$1 want=$2 got
    got="$(printf '%s\n' "$3" | stack_df_free_gib)"
    if [ "$got" != "$want" ]; then
        printf 'FAIL %s\n  want %s\n  got  %s\n' "$name" "$want" "$got"
        failed=1
    fi
}

# Real `df -Pk /` output from each system, 1 KiB blocks.
check "GNU coreutils (Linux)" 14 'Filesystem     1024-blocks      Used Available Capacity Mounted on
/dev/root         75085112  60313636  14755092      81% /'
check "BSD (macOS)" 125 'Filesystem   1024-blocks      Used Available Capacity  Mounted on
/dev/disk3s1s1 970969528  13631392 131310632    10% /'
check "less than one GiB" 0 'Filesystem 1024-blocks Used Available Capacity Mounted on
/dev/x 2000000 1500000 500000 75% /'

# And on this machine: a whole number, not empty and not an error.
here="$(stack_free_gib)"
if ! [[ "$here" =~ ^[0-9]+$ ]]; then
    printf 'FAIL this machine: stack_free_gib printed %q\n' "$here"
    failed=1
fi

[ "$failed" -eq 0 ] || exit 1
printf '%s\n' "stack disk: free space reads on Linux and macOS (here: ${here}GiB)"
