#!/usr/bin/env bash
#
# Shared writer for deploy/info.env — the single source for BOTH the backend
# and frontend `metadata:write` Taskfile tasks. Previously each Taskfile
# carried a near-identical copy of this logic; they drifted (the frontend
# copy grew OCI_REGISTRY + PROJECT_NAME lines the backend copy lacked), so a
# fix in one silently missed the other. This script is now the one place the
# format lives — mirrors how tasks/version.sh is the one place the version
# lives (which this script calls).
#
# Usage:
#   write-info-env.sh <output-file> [timezone]
#
# Environment (optional, written only when set non-empty):
#   OCI_REGISTRY   — image registry, e.g. registry.local
#   PROJECT_NAME   — chart/image name, e.g. paladin-core[-ui]
#
# APP_VERSION comes from tasks/version.sh (the version source of truth);
# GIT_COMMIT_HASH / BUILD_TIME are stamped here.
set -euo pipefail

out="${1:?output file path required}"
tz="${2:-UTC}"

root="$(git rev-parse --show-toplevel)"
ver="$(bash "${root}/tasks/version.sh")"

mkdir -p "$(dirname "${out}")"
{
	printf 'APP_VERSION=%s\n'     "${ver}"
	printf 'GIT_COMMIT_HASH=%s\n' "$(git rev-parse --short HEAD)"
	printf 'BUILD_TIME=%s\n'      "$(TZ="${tz}" date +%Y-%m-%dT%H:%M:%S%z)"
	if [ -n "${OCI_REGISTRY:-}" ]; then
		printf 'OCI_REGISTRY=%s\n' "${OCI_REGISTRY}"
	fi
	if [ -n "${PROJECT_NAME:-}" ]; then
		printf 'PROJECT_NAME=%s\n' "${PROJECT_NAME}"
	fi
} >"${out}"

printf 'INFO: %s: version %s\n' "${PROJECT_NAME:-app}" "${ver}"
