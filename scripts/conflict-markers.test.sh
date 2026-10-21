#!/usr/bin/env bash
# conflict-markers.test.sh — no tracked file keeps a merge's conflict markers.
#
# Two `||||||| parent of …` lines sat in docs/upgrading.md for months: a
# resolution that kept both sides and forgot the base's marker reads as prose
# in a diff, and nothing that renders Markdown complains.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no network.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

# A marker git writes: seven of its character at the start of a line, then a
# space and the side's name. `=======` alone is left out — Markdown underlines
# a heading with it.
readonly MARKERS='^(<{7}|>{7}|\|{7}) '
# This file is excluded: its comment spells a marker it refuses.
readonly SELF=scripts/conflict-markers.test.sh

# -I skips binary files; ls-files -z keeps a path with a space whole.
hits=$(git ls-files -z | grep -zvxF "$SELF" | xargs -0 grep -InE "$MARKERS" || true)
if [ -n "$hits" ]; then
    printf '%s\n%s\n' "!!! a merge's conflict markers are still in these files:" "$hits" >&2
    exit 1
fi
printf '%s\n' "conflict markers: none in the tracked files"
