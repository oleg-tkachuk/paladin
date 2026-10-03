#!/usr/bin/env bash
# task-offline.test.sh — scripts/task-offline.sh runs Task from a git hook
# without fetching anything and without the hook's git state.
#
# The hooks once set TASK_OFFLINE=1, which Task ignores, so every commit cloned
# the include repository; in a linked worktree the clone inherited
# GIT_INDEX_FILE and wrote that repository's paths into this one's index. Each
# case below pins one half of the fix, and the last keeps the hooks on it.
#
# Runs from `task -t Taskfile.dev.yaml verify-repo` — no network: a clone, if
# one slipped through, is pointed at a path that does not exist and fails.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
script="$root/scripts/task-offline.sh"
readonly HOOKS="$root/lefthook.yml"
# The components whose Taskfiles the hooks run, each with its own cache.
readonly COMPONENTS=(backend frontend)
# Remote includes the hooks name: any reference to github.com goes nowhere.
readonly NOWHERE=file:///nonexistent/
readonly GITHUB=https://github.com/
# Shorter than any age the cache can have, so every cached include is expired
# and only offline mode keeps Task from refreshing it.
readonly EXPIRED=1ns

work=$(mktemp -d "${TMPDIR:-/tmp}/task-offline-test.XXXXXX")
trap 'rm -rf "$work"' EXIT

failed=0
fail() { printf 'FAIL %s\n' "$1"; failed=1; }

# The state git hands a hook in a linked worktree, on a copy of the index so a
# write through it shows up here and nowhere else.
gitdir=$(git -C "$root" rev-parse --absolute-git-dir)
cp "$(git -C "$root" rev-parse --path-format=absolute --git-path index)" "$work/index"
cp "$work/index" "$work/index.before"
printf '[url "%s"]\n\tinsteadOf = %s\n' "$NOWHERE" "$GITHUB" >"$work/gitconfig"

# 1. What reaches `task`: the offline flag first, no repository variables.
mkdir -p "$work/stub"
cat >"$work/stub/task" <<'STUB'
#!/bin/sh
printf 'args %s\n' "$*"
env | grep '^GIT_' | sort
STUB
chmod +x "$work/stub/task"
seen=$(GIT_DIR="$gitdir" GIT_INDEX_FILE="$work/index" GIT_WORK_TREE="$root" \
    PATH="$work/stub:$PATH" "$script" codegen:sqlc:check)
grep -qx 'args --offline codegen:sqlc:check' <<<"$seen" ||
    fail "task was not called as \`task --offline <args>\`: $(head -n1 <<<"$seen")"
for var in $(git rev-parse --local-env-vars); do
    if grep -q "^$var=" <<<"$seen"; then
        fail "$var reached task"
    fi
done

# 2. The real Task, with every cached include expired and the hook's git state
# set: it must resolve from the committed cache — a clone it attempts fails on
# the unreachable URL — and leave the index as it found it.
for c in "${COMPONENTS[@]}"; do
    if ! out=$(cd "$root/$c" && GIT_CONFIG_GLOBAL="$work/gitconfig" \
        GIT_DIR="$gitdir" GIT_INDEX_FILE="$work/index" \
        "$script" --expiry "$EXPIRED" --list 2>&1); then
        fail "$c: task did not resolve its includes offline"
        printf '      %s\n' "${out//$'\n'/$'\n'      }"
    fi
done
cmp -s "$work/index" "$work/index.before" || fail "the index passed to the hook was rewritten"

# 3. Every hook that runs Task goes through the script. A bare `task` on a run
# line is the form that cloned.
bare=$(grep -nE '^[^#]*(^|[[:space:];&|(])task[[:space:]]' "$HOOKS" || true)
[ -z "$bare" ] || fail "lefthook.yml runs task without scripts/task-offline.sh: $bare"

[ "$failed" -eq 0 ] || exit 1
printf '%s\n' "task-offline: hooks run Task offline, without the hook's git state"
