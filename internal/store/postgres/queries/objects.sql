-- Object queries.

-- name: CreateObject :exec
INSERT INTO objects (
    object_id, tenant_id, object_key, key, state,
    content_type, size_bytes, checksum_algorithm, checksum,
    metadata, tags, external_ref, presign_expires_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9,
    $10, $11, $12, $13
);

-- name: GetObject :one
SELECT sqlc.embed(objects)
FROM objects
WHERE tenant_id = $1 AND object_id = $2;

-- name: LookupObjectByID :one
-- Reads an object by id alone. Used by background workers (reconciler,
-- replicator) that don't carry a tenant context. Joins object_keys to
-- materialize the bucket binding so the caller can call S3 in one trip.
SELECT o.object_id, o.tenant_id, o.object_key, o.key, o.state,
       b.backend_id, b.bucket_name
FROM objects o
JOIN object_keys b
  ON b.tenant_id = o.tenant_id AND b.object_key = o.object_key
WHERE o.object_id = $1;

-- name: LookupObjectByKey :one
-- Used by resource-name resolution: object_keys/{b}/objects-by-key/{key} → object_id.
SELECT sqlc.embed(objects)
FROM objects
WHERE tenant_id = $1 AND object_key = $2 AND key = $3 AND state <> 'DELETED';

-- name: PromoteObject :execrows
-- Idempotent promotion from PENDING → AVAILABLE. The sequencer guard keeps
-- out-of-order S3 events + reconciler + RPC calls from regressing state.
-- If AVAILABLE already, this is a no-op ONLY when the incoming sequencer is
-- strictly greater than the stored sequencer (or either is NULL).
UPDATE objects
SET state      = 'AVAILABLE',
    size_bytes = sqlc.arg('size_bytes'),
    etag       = sqlc.arg('etag'),
    checksum   = sqlc.narg('checksum'),
    sequencer  = sqlc.narg('sequencer'),
    committed_at = COALESCE(committed_at, now())
WHERE object_id = $1
  AND state IN ('PENDING', 'AVAILABLE')
  AND (sqlc.narg('sequencer')::text IS NULL
       OR sequencer IS NULL
       OR sqlc.narg('sequencer')::text > sequencer);

-- name: MarkObjectFailed :execrows
UPDATE objects
SET state         = 'FAILED',
    terminated_at = now()
WHERE object_id = $1
  AND state = 'PENDING';

-- name: SoftDeleteObject :execrows
-- expected_version=0 disables the OCC guard (force).
UPDATE objects
SET state         = 'DELETED',
    terminated_at = now()
WHERE tenant_id = $1 AND object_id = $2
  AND state IN ('AVAILABLE', 'PENDING')
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: RestoreObject :execrows
-- Undeletes a soft-deleted object iff no live row exists with the same
-- (tenant, object_key, key). Caller is expected to verify uniqueness first;
-- a UNIQUE partial index still catches the race at commit time.
UPDATE objects
SET state         = 'AVAILABLE',
    terminated_at = NULL
WHERE tenant_id = $1 AND object_id = $2
  AND state = 'DELETED';

-- name: HardDeleteObject :execrows
-- Removes the row outright. Caller is responsible for first deleting the
-- object from the storage backend (S3 DeleteObject). Allowed from any
-- state. expected_version=0 skips the OCC guard.
DELETE FROM objects
WHERE tenant_id = $1 AND object_id = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: CheckLiveCollision :one
-- True when a non-DELETED row already exists at (tenant, object_key, key).
-- Used by RestoreObject to refuse restoring into a slot that's been reused.
SELECT EXISTS(
    SELECT 1 FROM objects
    WHERE tenant_id = $1
      AND object_key = $2
      AND key = $3
      AND state <> 'DELETED'
)::boolean AS exists;

-- name: ListObjects :many
-- CEL filter is applied by the caller post-load. Keyset page uses object_id
-- (UUIDv7) which is monotonic-by-time.
SELECT sqlc.embed(objects)
FROM objects
WHERE tenant_id = $1
  AND object_key = $2
  AND (sqlc.narg('state')::object_state IS NULL OR state = sqlc.narg('state')::object_state)
  AND (sqlc.narg('prefix')::text IS NULL OR key LIKE sqlc.narg('prefix')::text || '%')
  AND (sqlc.narg('after_id')::uuid IS NULL OR object_id > sqlc.narg('after_id')::uuid)
ORDER BY object_id
LIMIT sqlc.arg('page_size');

-- name: CountObjects :one
SELECT COUNT(*) AS n
FROM objects
WHERE tenant_id = $1 AND object_key = $2
  AND (sqlc.narg('state')::object_state IS NULL OR state = sqlc.narg('state')::object_state);

-- name: ScanPendingExpired :many
-- Reconciler picks up PENDING rows whose presign has expired.
SELECT sqlc.embed(objects)
FROM objects
WHERE state = 'PENDING'
  AND presign_expires_at < now()
ORDER BY presign_expires_at
LIMIT sqlc.arg('batch_size');

-- name: UpdateObjectMetadata :execrows
UPDATE objects
SET metadata     = COALESCE(sqlc.narg('metadata'), metadata),
    tags         = COALESCE(sqlc.narg('tags'),     tags),
    external_ref = COALESCE(sqlc.narg('external_ref'), external_ref)
WHERE tenant_id = $1 AND object_id = $2
  AND state = 'AVAILABLE'
  AND resource_version = sqlc.arg('expected_version');
