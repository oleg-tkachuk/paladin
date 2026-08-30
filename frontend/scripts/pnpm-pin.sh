#!/usr/bin/env bash
# pnpm's version is pinned in two places that MUST agree, and neither of them
# is the version you happen to have installed:
#
#   frontend/package.json      "packageManager": "pnpm@X"  — local dev, and
#                              what pnpm/action-setup reads in CI
#   frontend/deploy/Dockerfile ARG PNPM_VERSION=X          — the image build
#
# The Dockerfile's own comment says it exists to match package.json. Nothing
# enforced that, and nothing kept either current: both sat at 11.21.0 while
# 11.24.0 was the latest stable and the local install had already moved.
#
# Renovate covers `packageManager` through its npm manager, but the Dockerfile
# ARG was invisible to it until the `# renovate:` annotation was added beside
# it. That closes the gap when Renovate runs; this script closes it now, and
# `check` keeps the two from drifting apart in between.
#
#   pnpm-pin.sh check     compare the two pins; fail if they disagree
#   pnpm-pin.sh latest    print the latest stable pnpm from npm
#   pnpm-pin.sh update    set both pins to the latest stable
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
pkg="$here/../package.json"
dockerfile="$here/../deploy/Dockerfile"

pin_pkg()    { sed -n 's/.*"packageManager"[[:space:]]*:[[:space:]]*"pnpm@\([^"]*\)".*/\1/p' "$pkg" | head -1; }
pin_docker() { sed -n 's/^ARG PNPM_VERSION=\(.*\)$/\1/p' "$dockerfile" | head -1; }

latest() {
  # The authoritative source, not whatever is installed locally. `latest` is
  # the GA dist-tag — pre-releases live under their own tags and are not
  # returned here.
  local v
  v="$(npm view pnpm version 2>/dev/null || true)"
  if [[ -z "$v" ]]; then
    echo "pnpm-pin: could not reach the npm registry" >&2
    return 1
  fi
  # Refuse anything that is not a plain GA triple, so a registry hiccup or a
  # tagged pre-release cannot be written into a pin.
  if [[ ! "$v" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "pnpm-pin: npm returned a non-GA version ($v); refusing to pin it" >&2
    return 1
  fi
  printf '%s\n' "$v"
}

case "${1:-check}" in
  latest) latest ;;

  check)
    a="$(pin_pkg)"; b="$(pin_docker)"
    if [[ -z "$a" || -z "$b" ]]; then
      echo "pnpm-pin: could not read both pins (package.json='$a' Dockerfile='$b')" >&2
      exit 1
    fi
    if [[ "$a" != "$b" ]]; then
      cat >&2 <<MSG
pnpm-pin: the two pins disagree.
  package.json  packageManager = pnpm@$a
  Dockerfile    ARG PNPM_VERSION = $b
The image would build with a different pnpm than local dev and CI use. Run
'task deps:update:pnpm' to bring both to the latest stable.
MSG
      exit 1
    fi
    echo "pnpm pinned at $a in both places"
    ;;

  update)
    v="$(latest)"
    cur="$(pin_pkg)"
    if [[ "$cur" == "$v" && "$(pin_docker)" == "$v" ]]; then
      echo "pnpm already pinned at $v"
      exit 0
    fi
    # In-place with a portable -i form: BSD sed (macOS) needs the empty suffix.
    sed -i.bak "s/\"packageManager\"[[:space:]]*:[[:space:]]*\"pnpm@[^\"]*\"/\"packageManager\": \"pnpm@$v\"/" "$pkg"
    sed -i.bak "s/^ARG PNPM_VERSION=.*/ARG PNPM_VERSION=$v/" "$dockerfile"
    rm -f "$pkg.bak" "$dockerfile.bak"
    echo "pnpm pinned: $cur -> $v (package.json + Dockerfile)"
    echo "Reinstall locally with: corepack use pnpm@$v   (or npm i -g pnpm@$v)"
    ;;

  *) echo "usage: pnpm-pin.sh [check|latest|update]" >&2; exit 2 ;;
esac
