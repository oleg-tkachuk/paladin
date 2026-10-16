-- Bucket grants (ADR-0032): the tenants other than its owner that may bind a
-- collection to a bucket. Platform configuration, outside row-level security,
-- like buckets themselves; the handler gates writes on the owner's
-- ManageBucket.

-- name: GrantBucket :one
-- Idempotent: granting a tenant that already holds a grant returns the grant
-- it holds, with its first granter and time.
INSERT INTO bucket_grants (bucket_id, tenant_id, granted_by)
SELECT b.id, sqlc.arg('tenant_id'), sqlc.arg('granted_by')
FROM buckets b
JOIN storage_backends sb ON sb.id = b.backend_id
WHERE sb.name = sqlc.arg('backend_name') AND b.name = sqlc.arg('bucket_name')
ON CONFLICT (bucket_id, tenant_id) DO UPDATE SET granted_by = bucket_grants.granted_by
RETURNING tenant_id, granted_by, granted_at;

-- name: RevokeBucketGrant :execrows
-- Removes a grant unless the tenant still has a collection in the bucket: the
-- grant is what lets those collections stay, and the binding trigger checks it
-- only when a collection is written. Zero rows: no such grant, or collections
-- remain — BucketGrantInUse tells the two apart.
DELETE FROM bucket_grants g
USING buckets b, storage_backends sb
WHERE g.bucket_id = b.id AND sb.id = b.backend_id
  AND sb.name = sqlc.arg('backend_name') AND b.name = sqlc.arg('bucket_name')
  AND g.tenant_id = sqlc.arg('tenant_id')
  AND NOT EXISTS (SELECT 1 FROM collections c
                   WHERE c.bucket_id = b.id AND c.tenant_id = g.tenant_id);

-- name: BucketGrantInUse :one
-- Whether the tenant holds a grant on the bucket and has a collection in it.
SELECT EXISTS (
    SELECT 1 FROM bucket_grants g
    JOIN buckets b ON b.id = g.bucket_id
    JOIN storage_backends sb ON sb.id = b.backend_id
    WHERE sb.name = sqlc.arg('backend_name') AND b.name = sqlc.arg('bucket_name')
      AND g.tenant_id = sqlc.arg('tenant_id')
      AND EXISTS (SELECT 1 FROM collections c
                   WHERE c.bucket_id = b.id AND c.tenant_id = g.tenant_id))::bool AS in_use;

-- name: ListBucketGrants :many
-- A bucket's grants, oldest first.
SELECT g.tenant_id, g.granted_by, g.granted_at
FROM bucket_grants g
JOIN buckets b ON b.id = g.bucket_id
JOIN storage_backends sb ON sb.id = b.backend_id
WHERE sb.name = sqlc.arg('backend_name') AND b.name = sqlc.arg('bucket_name')
ORDER BY g.granted_at, g.tenant_id;

-- name: TenantMayBindBucket :one
-- Whether a tenant may bind a collection to the bucket: it owns it or holds a
-- grant. The binding trigger enforces the same rule; this asks it first, so a
-- refusal is a clear answer rather than a failed write.
SELECT (b.owner_tenant_id = sqlc.arg('tenant_id')
        OR EXISTS (SELECT 1 FROM bucket_grants g
                    WHERE g.bucket_id = b.id AND g.tenant_id = sqlc.arg('tenant_id')))::bool AS may_bind
FROM buckets b
JOIN storage_backends sb ON sb.id = b.backend_id
WHERE sb.name = sqlc.arg('backend_name') AND b.name = sqlc.arg('bucket_name');
