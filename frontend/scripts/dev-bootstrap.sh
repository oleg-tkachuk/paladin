#!/usr/bin/env bash
# dev-bootstrap.sh — idempotently provision the resources the UI dev token
# expects to find on a fresh Paladin backend, plus a fully permissive Cedar
# policy so the dev tenant can do everything in its bucket.
#
# The dev JWT in configs/config.yaml carries:
#   tenant = 3a823fd4-0b3d-4ce2-a280-93b8d75cc07b
#   roles  = ["platform-admin"]
#
# After running, the UI can immediately list tenants/buckets/collections
# and exercise the full object lifecycle without any manual setup.
#
# Requires: curl, jq. Backend reachable at $PALADIN_HOST.
#
# The admin plane has its own hostname: the IngressRoute serves
# admin.paladin.local separately so a middleware chain can gate the whole
# plane, and the /paladin.admin.v1. prefix is NO LONGER routed on
# $PALADIN_HOST. `call` dispatches on the procedure prefix, so call sites
# stay unchanged. Override $PALADIN_ADMIN_HOST for a deployment that keeps
# admin on the shared host (chart default: traefik.planes.admin.hosts empty).

set -euo pipefail

PALADIN_HOST="${PALADIN_HOST:-https://api.paladin.local}"
PALADIN_ADMIN_HOST="${PALADIN_ADMIN_HOST:-https://admin.paladin.local}"
TENANT_ID="${TENANT_ID:-3a823fd4-0b3d-4ce2-a280-93b8d75cc07b}"
BACKEND_ID="${BACKEND_ID:-primary}"
BUCKET_ID="${BUCKET_ID:-paladin-primary}"
COLLECTION="${COLLECTION:-default}"
JWT="${JWT:-}"

if [[ -z "$JWT" ]]; then
    JWT=$(awk '/devToken:/ {gsub(/"/,""); print $2}' configs/config.yaml | head -n1)
fi
if [[ -z "$JWT" ]]; then
    echo "no JWT (set \$JWT or have configs/config.yaml.devToken)" >&2
    exit 1
fi

# ─── HTTP helpers ────────────────────────────────────────────────────────────

# call <method> <body> [idempotency-key]
#
# Every Create*/Issue* RPC is rejected outright without an Idempotency-Key
# (middleware/idempotency.go, RequireOnCreate) — the header is not optional
# and not a nicety. This script had no idea: it sent none, so the very first
# CreateTenant failed and nothing after it ran.
#
# The key is passed in rather than generated here, because it must be the
# same on every run. A fresh random key each time would re-enter the handler
# and lean on the create colliding, which happens to work only because
# `ensure` forgives already_exists; a key derived from the resource identity
# replays the original response instead, which is what idempotent means.
call() {
    local method=$1 body=$2 idem=${3:-}
    local host="$PALADIN_HOST"
    case "$method" in
        paladin.admin.v1.*) host="$PALADIN_ADMIN_HOST" ;;
    esac
    curl -k -s -X POST "$host/$method" \
        -H "Content-Type: application/json" \
        -H "Connect-Protocol-Version: 1" \
        -H "Authorization: Bearer $JWT" \
        -H "X-Tenant-ID: $TENANT_ID" \
        ${idem:+-H "Idempotency-Key: $idem"} \
        -d "$body"
}

# ensure <label> <method> <body> <idempotency-key>
ensure() {
    local label=$1 method=$2 body=$3 idem=$4
    local resp code msg
    resp=$(call "$method" "$body" "$idem")
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
    rv=$(fetch_resource_version paladin.admin.v1.TenantService/GetTenant "tenants/$TENANT_ID")
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
           tenant: { inherited_cedar_policy: $policy }
         }')
    local resp code
    resp=$(call paladin.admin.v1.TenantService/UpdateTenant "$body")
    code=$(echo "$resp" | jq -r '.code // empty')
    if [[ -z "$code" ]]; then
        echo "  ✓ tenant cedar policy (permissive)"
    else
        echo "  ✗ tenant cedar policy: $resp" >&2
        return 1
    fi
}

# patch_collection_policy
# Same idea, but writes to the Collection's own policy field. Belt-and-
# suspenders so even if tenant inheritance is broken or scoped, the
# collection still authorizes everything.
patch_collection_policy() {
    local rv
    rv=$(fetch_resource_version paladin.admin.v1.CollectionService/GetCollection \
         "tenants/$TENANT_ID/collections/$COLLECTION")
    if [[ -z "$rv" ]]; then
        echo "  ✗ collection policy: could not fetch resource_version" >&2
        return 1
    fi
    local policy_json
    policy_json=$(build_permissive_policy | jq -Rs .)
    local body
    body=$(jq -nc \
        --arg name "tenants/$TENANT_ID/collections/$COLLECTION" \
        --arg rv "$rv" \
        --argjson policy "$policy_json" \
        '{
           name: $name,
           resource_version: $rv,
           cedar_policy: $policy
         }')
    local resp code
    resp=$(call paladin.admin.v1.CollectionService/SetCollectionPolicy "$body")
    code=$(echo "$resp" | jq -r '.code // empty')
    if [[ -z "$code" ]]; then
        echo "  ✓ collection cedar policy (permissive)"
    else
        echo "  ✗ collection cedar policy: $resp" >&2
        return 1
    fi
}

# ─── Run ─────────────────────────────────────────────────────────────────────

echo "─── Bootstrapping dev tenant against $PALADIN_HOST ───"

# 1. Resources
ensure "tenant" paladin.admin.v1.TenantService/CreateTenant \
    "$(jq -nc --arg tid "$TENANT_ID" \
        '{tenant_id: $tid,
          tenant: {slug: "ui-dev", display_name: "UI dev tenant"}}')" \
    "dev-bootstrap-tenant-$TENANT_ID"

ensure "bucket" paladin.admin.v1.BucketService/CreateBucket \
    "$(jq -nc --arg be "$BACKEND_ID" --arg b "$BUCKET_ID" \
        '{parent: ("storageBackends/" + $be),
          bucket_id: $b,
          bucket: {display_name: "primary"}}')" \
    "dev-bootstrap-bucket-$BACKEND_ID-$BUCKET_ID"

ensure "collection" paladin.admin.v1.CollectionService/CreateCollection \
    "$(jq -nc --arg tid "$TENANT_ID" --arg c "$COLLECTION" \
        --arg be "$BACKEND_ID" --arg b "$BUCKET_ID" \
        '{parent: ("tenants/" + $tid),
          collection: $c,
          collection_resource: {
            tenant_id: $tid,
            collection: $c,
            display_name: "default namespace",
            bucket: ("storageBackends/" + $be + "/buckets/" + $b)
          }}')" \
    "dev-bootstrap-collection-$TENANT_ID-$COLLECTION"

# 2. Permissive Cedar policy at both scopes
patch_tenant_policy
patch_collection_policy

echo
echo "Done. The UI can now talk to the backend AND perform every operation"
echo "(upload, delete, copy, restore, …) on bucket=$BUCKET_ID / collection=$COLLECTION."
