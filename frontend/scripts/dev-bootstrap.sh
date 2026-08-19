#!/usr/bin/env bash
# dev-bootstrap.sh — idempotently provision the resources the UI dev token
# expects to find on a fresh Paladin backend, plus a fully permissive Cedar
# policy so the dev tenant can do everything in its bucket.
#
# The dev JWT in configs/config.yaml carries:
#   tenant = 3a823fd4-0b3d-4ce2-a280-93b8d75cc07b
#   roles  = ["platform-admin"]
#
# After running, the UI can immediately list tenants/buckets/object-keys
# and exercise the full object lifecycle without any manual setup.
#
# Requires: curl, jq. Backend reachable at $PALADIN_HOST.

set -euo pipefail

PALADIN_HOST="${PALADIN_HOST:-https://api.paladin.local}"
TENANT_ID="${TENANT_ID:-3a823fd4-0b3d-4ce2-a280-93b8d75cc07b}"
BACKEND_ID="${BACKEND_ID:-primary}"
BUCKET_NAME="${BUCKET_NAME:-paladin-primary}"
OBJECT_KEY="${OBJECT_KEY:-default}"
JWT="${JWT:-}"

if [[ -z "$JWT" ]]; then
    JWT=$(awk '/devToken:/ {gsub(/"/,""); print $2}' configs/config.yaml | head -n1)
fi
if [[ -z "$JWT" ]]; then
    echo "no JWT (set \$JWT or have configs/config.yaml.devToken)" >&2
    exit 1
fi

# ─── HTTP helpers ────────────────────────────────────────────────────────────

call() {
    local method=$1 body=$2
    curl -k -s -X POST "$PALADIN_HOST/$method" \
        -H "Content-Type: application/json" \
        -H "Connect-Protocol-Version: 1" \
        -H "Authorization: Bearer $JWT" \
        -H "X-Tenant-ID: $TENANT_ID" \
        -d "$body"
}

ensure() {
    local label=$1 method=$2 body=$3
    local resp code msg
    resp=$(call "$method" "$body")
    code=$(echo "$resp" | jq -r '.code // empty')
    msg=$(echo "$resp" | jq -r '.message // empty')
    if [[ -z "$code" ]] || [[ "$code" == "already_exists" ]] || [[ "$msg" == *"duplicate key"* ]]; then
        echo "  ✓ $label"
    else
        echo "  ✗ $label: $resp"
        exit 1
    fi
}

# ─── Cedar policy ────────────────────────────────────────────────────────────
#
# Maximally permissive dev policy: every action on every resource for any
# principal in the tenant. Production deployments should NEVER ship this —
# tenant.inherited_cedar_policy is precisely where you'd add real guards
# (size limits, file-extension blocklists, role gates on destructive ops).
#
# The dev policy is regenerated for the current $TENANT_ID so the principal
# scope matches the JWT's `tenant` claim.

build_permissive_policy() {
    cat <<EOF
// dev-bootstrap.sh: fully permissive policy for the Paladin dev tenant.
// DO NOT USE IN PRODUCTION — overrides every guard the default policy puts
// on uploads, deletes, copies, and admin operations.

permit (
    principal in Tenant::"$TENANT_ID",
    action,
    resource
);

// Mirror the same blanket allow for the synthetic dev-user subject so that
// JWTs missing the tenant claim still resolve. Cedar evaluates the union
// of matching permits.
permit (
    principal == User::"dev-user",
    action,
    resource
);
EOF
}

# fetch_resource_version <method> <name>
# Calls the right Get RPC and prints the resource_version field.
fetch_resource_version() {
    local method=$1 name=$2
    call "$method" "{\"name\":\"$name\"}" | jq -r '.resourceVersion // empty'
}

# patch_tenant_policy
# Updates tenant.inherited_cedar_policy via TenantService/UpdateTenant with
# OCC. Polls Get to grab the current resource_version first.
patch_tenant_policy() {
    local rv
    rv=$(fetch_resource_version paladin.v1.TenantService/GetTenant "tenants/$TENANT_ID")
    if [[ -z "$rv" ]]; then
        echo "  ✗ tenant policy: could not fetch resource_version" >&2
        return 1
    fi
    local policy_json
    policy_json=$(build_permissive_policy | jq -Rs .)
    local body
    # google.protobuf.FieldMask serializes to a comma-separated string of
    # camelCase JSON field names on the wire (canonical protojson form).
    # Snake_case ("inherited_cedar_policy") is rejected as invalid; the
    # object form ({ paths: [...] }) is also rejected by the unmarshaler.
    body=$(jq -nc \
        --arg name "tenants/$TENANT_ID" \
        --arg rv "$rv" \
        --argjson policy "$policy_json" \
        '{
           name: $name,
           resource_version: $rv,
           update_mask: "inheritedCedarPolicy",
           inherited_cedar_policy: $policy
         }')
    local resp code
    resp=$(call paladin.v1.TenantService/UpdateTenant "$body")
    code=$(echo "$resp" | jq -r '.code // empty')
    if [[ -z "$code" ]]; then
        echo "  ✓ tenant cedar policy (permissive)"
    else
        echo "  ✗ tenant cedar policy: $resp" >&2
        return 1
    fi
}

# patch_object_key_policy
# Same idea, but writes to the ObjectKey's own policy field. Belt-and-
# suspenders so even if tenant inheritance is broken or scoped, the
# object_key still authorizes everything.
patch_object_key_policy() {
    local rv
    rv=$(fetch_resource_version paladin.v1.ObjectKeyService/GetObjectKey "object_keys/$OBJECT_KEY")
    if [[ -z "$rv" ]]; then
        echo "  ✗ object_key policy: could not fetch resource_version" >&2
        return 1
    fi
    local policy_json
    policy_json=$(build_permissive_policy | jq -Rs .)
    local body
    body=$(jq -nc \
        --arg name "object_keys/$OBJECT_KEY" \
        --arg rv "$rv" \
        --argjson policy "$policy_json" \
        '{
           name: $name,
           resource_version: $rv,
           update_mask: "policy",
           policy: { cedar_policy: $policy, lifecycle_rules: [] }
         }')
    local resp code
    resp=$(call paladin.v1.ObjectKeyService/UpdateObjectKey "$body")
    code=$(echo "$resp" | jq -r '.code // empty')
    if [[ -z "$code" ]]; then
        echo "  ✓ object_key cedar policy (permissive)"
    else
        echo "  ✗ object_key cedar policy: $resp" >&2
        return 1
    fi
}

# ─── Run ─────────────────────────────────────────────────────────────────────

echo "─── Bootstrapping dev tenant against $PALADIN_HOST ───"

# 1. Resources
ensure "tenant"     paladin.v1.TenantService/CreateTenant       "{\"tenant_id\":\"$TENANT_ID\",\"display_name\":\"UI dev tenant\"}"
ensure "bucket"     paladin.v1.BucketService/CreateBucket       "{\"backend_id\":\"$BACKEND_ID\",\"bucket_name\":\"$BUCKET_NAME\",\"display_name\":\"primary\"}"
ensure "object_key" paladin.v1.ObjectKeyService/CreateObjectKey "{\"object_key\":\"$OBJECT_KEY\",\"backend_id\":\"$BACKEND_ID\",\"bucket_name\":\"$BUCKET_NAME\",\"display_name\":\"default namespace\"}"

# 2. Permissive Cedar policy at both scopes
patch_tenant_policy
patch_object_key_policy

echo
echo "Done. The UI can now talk to the backend AND perform every operation"
echo "(upload, delete, copy, restore, …) on bucket=$BUCKET_NAME / key=$OBJECT_KEY."
