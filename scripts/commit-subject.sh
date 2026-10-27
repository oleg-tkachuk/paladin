#!/usr/bin/env bash
# commit-subject.sh — the commit-msg gate: a subject in Conventional Commits.
#
# semantic-release reads the type off every subject on main, so one that does
# not parse silently releases nothing. A merge or a revert is git's own
# subject and passes. So does `git commit --fixup`'s: `fixup! `, `squash! `
# or `amend! ` before a subject that parses. Those commits exist to be folded
# by `git rebase -i --autosquash` before a push, and
# scripts/no-autosquash-commits.sh refuses to push one that was not.
#
# Usage: commit-subject.sh <message-file>

set -euo pipefail

readonly CC_RE='^(feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert|security)(\([A-Za-z0-9._/-]+\))?(!)?: .+'
# Git's own subjects.
readonly GIT_RE='^(Merge|Revert)\b'
# The prefixes `git commit --fixup` / `--squash` write, any number of them.
readonly AUTOSQUASH_RE='^((fixup|squash|amend)! )+'

file="${1:-}"
[ -f "$file" ] || exit 0

subject="$(head -n1 "$file" | tr -d '\r')"
printf '%s\n' "$subject" | grep -Eq "$GIT_RE" && exit 0

folded="$(printf '%s\n' "$subject" | sed -E "s/$AUTOSQUASH_RE//")"
if ! printf '%s\n' "$folded" | grep -Eq "$CC_RE"; then
    echo "ERROR: invalid commit message format (Conventional Commits required)." >&2
    echo "Format: <type>(<scope>)!: <subject>" >&2
    echo "See:    https://www.conventionalcommits.org/en/v1.0.0/" >&2
    exit 1
fi
