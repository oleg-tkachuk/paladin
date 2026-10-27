#!/usr/bin/env bash
# no-autosquash-commits.sh — the pre-push gate: no fixup!, squash! or amend!
# commit leaves the machine.
#
# commit-subject.sh lets them be written, for `git rebase -i --autosquash` to
# fold. One pushed unfolded reaches main under a subject semantic-release
# does not read, and the change it carries is never released.
#
# Usage: no-autosquash-commits.sh [base]   (default: the merge base with origin/main)

set -euo pipefail

readonly AUTOSQUASH_RE='^(fixup|squash|amend)! '
readonly DEFAULT_UPSTREAM=origin/main

base="${1:-}"
if [ -z "$base" ]; then
    if ! base="$(git merge-base HEAD "$DEFAULT_UPSTREAM" 2>/dev/null)"; then
        echo "no-autosquash-commits: no $DEFAULT_UPSTREAM to compare with; skipping" >&2
        exit 0
    fi
fi

# Hash, a tab, the subject: the pattern is matched against the subject alone.
pending="$(git log --format='%h%x09%s' "$base..HEAD" | awk -F '\t' -v re="$AUTOSQUASH_RE" '$2 ~ re')"
if [ -n "$pending" ]; then
    printf '%s\n%s\n%s\n' \
        "!!! these commits are meant to be folded before a push:" "$pending" \
        "fold them: git rebase -i --autosquash $base" >&2
    exit 1
fi
