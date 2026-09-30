#!/usr/bin/env bash
# checkov-config.test.sh — `checkov:scan` must be told what to scan.
#
# The checkov module's scan passes checkov the config file and CHECKOV_FLAGS
# and nothing else. With no `directory` and `framework` in .checkov.yaml,
# checkov prints its banner, scans nothing and exits 0 — the gate stayed green
# that way from the library's v11 bump until it was noticed. `checkov:triage`
# reads CHECKOV_TARGET and CHECKOV_FRAMEWORKS from Taskfile.dev.yaml instead,
# so the two must also name the same things, or the triage view shows findings
# from a different scan than the gate's.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
readonly CONFIG="$root/.checkov.yaml"
readonly TASKFILE="$root/Taskfile.dev.yaml"
readonly INCLUDE_VARS=.includes.checkov.vars

sorted() { yq -o=json "$1" "$2" | jq -c 'if type == "array" then sort else . end'; }

fail=0
check() {
    local what=$1 config_key=$2 taskfile_key=$3
    local from_config from_taskfile
    from_config=$(sorted ".\"$config_key\"" "$CONFIG")
    from_taskfile=$(sorted "$INCLUDE_VARS.$taskfile_key" "$TASKFILE")
    if [[ "$from_config" == null || "$from_config" == "[]" ]]; then
        echo "!!! .checkov.yaml names no $what: checkov:scan would scan nothing and pass" >&2
        fail=1
    elif [[ "$from_config" != "$from_taskfile" ]]; then
        echo "!!! $what: .checkov.yaml has $from_config, $taskfile_key has $from_taskfile" >&2
        fail=1
    fi
}

check directories directory CHECKOV_TARGET
check frameworks framework CHECKOV_FRAMEWORKS

[[ "$fail" == 0 ]] || exit 1
echo "checkov: .checkov.yaml and the triage view name the same directories and frameworks"
