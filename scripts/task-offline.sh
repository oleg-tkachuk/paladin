#!/usr/bin/env bash
# task-offline.sh — run `task` from a git hook: every remote include resolved
# from the committed .task/remote cache, and none of the hook's git state.
#
#   ../scripts/task-offline.sh codegen:sqlc:check     # from backend/ or frontend/
#
# --offline, because Task 3.x reads only the flag: a TASK_OFFLINE variable in
# the environment is ignored, and an expired cache is then refreshed by cloning
# the include repository into Task's shared task-git-repos directory — one per
# repo+ref for every process on the machine, with no lock. Offline, an expired
# cache is used as it is and a missing one fails loudly instead of being fetched.
#
# The git variables, because git exports them to hooks — GIT_DIR and
# GIT_INDEX_FILE in a linked worktree — and every git Task runs inherits them.
# A clone that inherited GIT_INDEX_FILE wrote the include repository's paths
# into this repository's index, referencing blobs its object store does not
# have, and the next commit failed on `invalid object`. Without them git finds
# the repository from the working directory, as it would in a terminal.

set -euo pipefail

# git's own list of the variables that point it at a repository, rather than a
# copy of it here that misses the next one git adds.
# shellcheck disable=SC2046 # one variable name per word is the point
unset $(git rev-parse --local-env-vars)

exec task --offline "$@"
