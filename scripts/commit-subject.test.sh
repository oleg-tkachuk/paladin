#!/usr/bin/env bash
# commit-subject.test.sh — the commit-msg and pre-push subject gates.
#
# commit-subject.sh takes a Conventional Commits subject, git's own, or one
# `git commit --fixup` wrote over a subject that parses; no-autosquash-commits.sh
# refuses to push a branch still carrying a fixup!, squash! or amend! commit.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no network.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
readonly SUBJECT="$root/scripts/commit-subject.sh"
readonly AUTOSQUASH="$root/scripts/no-autosquash-commits.sh"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
failures=0

check_subject() { # want(pass|fail) subject
    local want=$1 subject=$2 got=pass
    printf '%s\n\nbody\n' "$subject" > "$work/msg"
    "$SUBJECT" "$work/msg" 2>/dev/null || got=fail
    if [ "$got" != "$want" ]; then
        printf 'commit-subject: %-60s got %s, want %s\n' "\"$subject\"" "$got" "$want" >&2
        failures=$((failures + 1))
    fi
}

check_subject pass 'feat(console): list a policy'"'"'s earlier versions'
check_subject pass 'fix: a thing'
check_subject pass 'feat!: drop the old field'
check_subject pass 'fixup! feat(api): serve a policy layer'"'"'s earlier versions'
check_subject pass 'squash! fix: a thing'
check_subject pass 'amend! docs(adr): keep the name'
check_subject pass 'fixup! fixup! test: one import'
check_subject pass 'Merge pull request #1 from a/b'
check_subject pass 'Revert "feat: x"'
check_subject fail 'fixup! not a conventional subject'
check_subject fail 'fixup!feat: no space after the prefix'
check_subject fail 'Fixup! feat: wrong case'
check_subject fail 'update stuff'
check_subject fail 'feat:no space'

# A repository with a base commit, then a branch on it.
repo="$work/repo"
git init -q -b main "$repo"
git -C "$repo" -c user.name=t -c user.email=t@t commit -q --allow-empty -m "chore: base"
base=$(git -C "$repo" rev-parse HEAD)
commit() { git -C "$repo" -c user.name=t -c user.email=t@t commit -q --allow-empty -m "$1"; }

check_push() { # want(pass|fail) what
    local want=$1 what=$2 got=pass
    (cd "$repo" && "$AUTOSQUASH" "$base") 2>/dev/null || got=fail
    if [ "$got" != "$want" ]; then
        printf 'no-autosquash-commits: %s: got %s, want %s\n' "$what" "$got" "$want" >&2
        failures=$((failures + 1))
    fi
}

commit "feat: a change"
check_push pass "a branch of ordinary commits"
commit "fixup! feat: a change"
check_push fail "a branch with a fixup! commit"
git -C "$repo" reset -q --hard HEAD~1
commit "feat: mention fixup! in passing"
check_push pass "fixup! inside a subject, not before it"
commit "squash! feat: a change"
check_push fail "a branch with a squash! commit"

if [ "$failures" -ne 0 ]; then
    printf '!!! %d commit-subject case(s) failed\n' "$failures" >&2
    exit 1
fi
printf '%s\n' "commit subjects: every case answered as expected"
