#!/usr/bin/env bash
# health-token-secret.test.sh — in production the health snapshot token comes
# from one Secret, and both charts read the same one.
#
# The backend gates /system/health.json with runtime.health_snapshot_token; the
# console's BFF forwards the token it is given. Three things must hold in the
# rendered production manifests, and none of them fails loudly when it breaks:
#
#   - the backend's config names the Secret and carries no inline token, so the
#     token is not in the ConfigMap;
#   - the backend may GET that Secret — otherwise boot fails with a 403;
#   - the console mounts the same Secret and key as a file and is told where —
#     otherwise its health page gets 401s from every backend.
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

# The console mounts "<secret>:<key>" at some path and names "<path>/<key>".
spec=$(render "$frontend" | yq -o=json "select(.kind == \"Deployment\") | .spec.template.spec")
# shellcheck disable=SC2016 # $v is a yq variable, not a shell one
mounted=$(printf '%s\n' "$spec" | yq -r '
    .volumes[] | select(.secret) | .name as $v | .secret.secretName + ":" + (.secret.items[].key) + " " + $v')
file_env=$(printf '%s\n' "$spec" | yq -r ".containers[].env[] | select(.name == \"$TOKEN_FILE_ENV\") | .value")
volume=$(grep -F -- "$name:$key " <<<"$mounted" | cut -d' ' -f2 || true)
if [ -z "$volume" ]; then
    fail "the console mounts no '$key' from Secret '$name'"
else
    mount_path=$(printf '%s\n' "$spec" | yq -r ".containers[].volumeMounts[] | select(.name == \"$volume\") | .mountPath")
    [ "$file_env" = "$mount_path/$key" ] ||
        fail "the console reads the token from '$file_env', the Secret is mounted at '$mount_path/$key'"
fi

if [ "$failed" -ne 0 ]; then
    exit 1
fi
printf '%s\n' "health token: both charts read Secret $name/$key in production; none inline"
