#!/usr/bin/env bash
# release-age.test.sh — Renovate must not propose an npm release pnpm would
# refuse to install.
#
# pnpm holds back any release younger than minimumReleaseAge
# (frontend/pnpm-workspace.yaml), and --frozen-lockfile fails on a lockfile
# naming one. Renovate proposed updates the day they were published, so every
# install in CI failed until the release aged past pnpm's window. Its own
# minimumReleaseAge for npm (renovate.json) must be at least pnpm's.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
readonly PNPM_CONFIG="$root/frontend/pnpm-workspace.yaml"
readonly RENOVATE_CONFIG="$root/renovate.json"
readonly MINUTES_PER_HOUR=60
readonly MINUTES_PER_DAY=$((24 * MINUTES_PER_HOUR))

# minutes <duration>: a Renovate duration ("1 day", "36 hours") in minutes.
minutes() {
    local n unit
    read -r n unit <<<"$1"
    [[ $n =~ ^[0-9]+$ ]] || { echo "!!! not a duration: $1" >&2; return 1; }
    case $unit in
    minute | minutes) echo "$n" ;;
    hour | hours) echo $((n * MINUTES_PER_HOUR)) ;;
    day | days) echo $((n * MINUTES_PER_DAY)) ;;
    *) echo "!!! unknown unit in: $1" >&2; return 1 ;;
    esac
}

# The parser, so a unit it misreads cannot pass the check below.
for case in "1 day=$MINUTES_PER_DAY" "2 days=$((2 * MINUTES_PER_DAY))" "36 hours=$((36 * MINUTES_PER_HOUR))" "90 minutes=90"; do
    got=$(minutes "${case%=*}")
    [ "$got" = "${case#*=}" ] || { echo "FAIL minutes '${case%=*}' = $got, want ${case#*=}" >&2; exit 1; }
done
if minutes "1 week" 2>/dev/null; then
    echo "FAIL an unknown unit was read" >&2
    exit 1
fi

pnpm_age=$(yq -e '.minimumReleaseAge' "$PNPM_CONFIG")
renovate_age=$(jq -er '[.packageRules[] | select(.matchManagers == ["npm"] and .minimumReleaseAge) | .minimumReleaseAge] | first' "$RENOVATE_CONFIG")
renovate_minutes=$(minutes "$renovate_age")

if ((renovate_minutes < pnpm_age)); then
    echo "FAIL renovate waits $renovate_age ($renovate_minutes min) for npm, pnpm refuses releases younger than $pnpm_age min" >&2
    exit 1
fi
echo "ok   renovate waits $renovate_age for npm, pnpm $pnpm_age min"
