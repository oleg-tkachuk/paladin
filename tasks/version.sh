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
#
# `api/*` tags are EXCLUDED: those version the proto contract (consumed by
# external repos via `buf generate <repo>.git#tag=api/vX`), not the PALADIN
# image/chart. They carry a `/` that is invalid in a Docker tag, and a
# contract bump must not restamp the image. Never let them leak in here.
#
# The short SHA is prefixed with `g` (the git-describe convention:
# `v1.2.3-4-gabc1234`). Without it, an all-numeric short SHA with a leading
# zero (e.g. `0825472`) is an invalid SemVer-2 pre-release identifier —
# "numeric identifiers MUST NOT include leading zeroes" — and Helm/OCI reject
# the chart version with `version segment starts with 0`. The `g` makes the
# segment alphanumeric, so the leading-zero rule never applies. (Observed on
# real all-digit SHAs; do not drop the prefix.)
set -eu

BASE="${PALADIN_VERSION_BASE:-0.1.0}"
TS=$(date +%s)
COMMIT=g$(git rev-parse --short HEAD)
DIRTY=$(git status --porcelain | wc -l | tr -d ' ')
TAG=$(git describe --tags --exact-match --exclude 'api/*' 2>/dev/null | sed 's/^v//' || true)

if [ -n "$TAG" ] && [ "$DIRTY" = "0" ]; then
  echo "$TAG"
elif [ -n "$TAG" ]; then
  echo "${TAG}-dev.${TS}.${COMMIT}"
else
  echo "${BASE}-dev.${TS}.${COMMIT}"
fi
