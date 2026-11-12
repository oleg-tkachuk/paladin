#!/usr/bin/env bash
# health-token-secret.test.sh — in production the health snapshot token comes
# from one Secret, which the backend reads and the console does not.
#
# The backend gates /system/health.json with runtime.health_snapshot_token, and
# its admin role reads every role's snapshot with it for the console's health
# page (GetPlatformHealth). Three things must hold in the rendered production
# manifests, and none of them fails loudly when it breaks:
#
#   - the backend's config names the Secret and carries no inline token, so the
#     token is not in the ConfigMap;
#   - the backend may GET that Secret — otherwise boot fails with a 403;
#   - the console holds no part of it: it mounts no Secret and is told of no
#     token file (ADR-0048).
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — helm and yq, no cluster.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
backend="$root/backend/deploy/chart"
frontend="$root/frontend/deploy/chart"
readonly PROD_VALUES=values-prod.yaml
readonly REQUIRED_VALUES=ci/required-values.yaml
readonly CONFIG_KEY=config.yaml
readonly TOKEN_FILE_ENV=PALADIN_HEALTH_SNAPSHOT_TOKEN_FILE

for tool in helm yq; do
    command -v "$tool" >/dev/null 2>&1 || { echo "!!! $tool is not installed" >&2; exit 1; }
done

render() { # chart dir
    local extra=()
    [ -f "$1/$REQUIRED_VALUES" ] && extra=(-f "$1/$REQUIRED_VALUES")
    helm template probe "$1" -f "$1/$PROD_VALUES" ${extra[@]+"${extra[@]}"}
}

failed=0
fail() { printf 'FAIL %s\n' "$1"; failed=1; }

backend_docs=$(render "$backend")
config=$(printf '%s\n' "$backend_docs" | yq -r "select(.kind == \"ConfigMap\" and .data[\"$CONFIG_KEY\"]) | .data[\"$CONFIG_KEY\"]")
inline=$(printf '%s\n' "$config" | yq -r '.runtime.health_snapshot_token // ""')
name=$(printf '%s\n' "$config" | yq -r '.runtime.health_snapshot_token_secret.name // ""')
key=$(printf '%s\n' "$config" | yq -r '.runtime.health_snapshot_token_secret.key // ""')

[ -z "$inline" ] || fail "the backend's config carries an inline health token"
if [ -z "$name" ] || [ -z "$key" ]; then
    fail "the backend's config names no health token Secret"
fi

readable=$(printf '%s\n' "$backend_docs" |
    yq -r 'select(.kind == "Role" or .kind == "ClusterRole") | .rules[].resourceNames[]')
grep -qxF -- "$name" <<<"$readable" ||
    fail "the backend may not GET Secret '$name' (rbac.secretReader.secretNames)"

# The console holds no backend secret: no Secret volume, no token file.
spec=$(render "$frontend" | yq -o=json "select(.kind == \"Deployment\") | .spec.template.spec")
secrets=$(printf '%s\n' "$spec" | yq -r '[.volumes[]? | select(.secret) | .secret.secretName] | join(",")')
file_env=$(printf '%s\n' "$spec" | yq -r ".containers[].env[] | select(.name == \"$TOKEN_FILE_ENV\") | .value")
[ -z "$secrets" ] || fail "the console mounts Secrets '$secrets'; it holds none"
[ -z "$file_env" ] || fail "the console is told of a health token file '$file_env'; it reads none"

if [ "$failed" -ne 0 ]; then
    exit 1
fi
printf '%s\n' "health token: the backend reads Secret $name/$key in production, none inline; the console holds none"
