#!/usr/bin/env bash
# Single source of truth for image + Helm-chart versions across EVERY
# Taskfile in this repo (backend + frontend). One mechanism, one pattern
# — invoked as
#   _VERSION: {sh: 'bash "$(git rev-parse --show-toplevel)/tasks/version.sh"'}
# so it resolves from any depth / working directory.
#
# Output is always SemVer-2 compliant (Helm OCI requires it). The dev
# pre-release is TS-FIRST (`-dev.<unix-ts>.<sha>`) so the newest
# wall-clock build always sorts highest — ArgoCD/Helm pick the latest
# push. A SHA-first (or timestamp-less) form lets an alphabetically
# higher SHA beat a newer build, which would leave ArgoCD pinned to a
# stale chart; never reintroduce that.
#
# Scheme:
#   exact tag + clean tree → "<tag>"                  (release)
#   exact tag + dirty tree → "<tag>-dev.<ts>.<sha>"   (dirty release)
#   no exact tag           → "<base>-dev.<ts>.<sha>"  (routine dev)
#
# `PALADIN_VERSION_BASE` overrides the no-tag base (default 0.1.0 — matches
# the pre-version.sh info.env seed).
set -eu

BASE="${PALADIN_VERSION_BASE:-0.1.0}"
TS=$(date +%s)
COMMIT=$(git rev-parse --short HEAD)
DIRTY=$(git status --porcelain | wc -l | tr -d ' ')
TAG=$(git describe --tags --exact-match 2>/dev/null | sed 's/^v//' || true)

if [ -n "$TAG" ] && [ "$DIRTY" = "0" ]; then
  echo "$TAG"
elif [ -n "$TAG" ]; then
  echo "${TAG}-dev.${TS}.${COMMIT}"
else
  echo "${BASE}-dev.${TS}.${COMMIT}"
fi
