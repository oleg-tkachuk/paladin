-- Object queries.

-- name: CreateObject :exec
-- collection_id is resolved by the caller via ResolveCollectionID.
INSERT INTO objects (
    id, tenant_id, collection_id, path, state,
    content_type, size_bytes, checksum_algorithm, checksum,
    metadata, tags, external_ref, presign_expires_at
) VALUES (
    $1, $2, $3, $4, $5::object_state,
    $6, $7, $8, $9,
    $10, $11, $12, $13
);

-- name: ResolveCollectionID :one
SELECT id FROM collections WHERE tenant_id = $1 AND name = $2;

-- name: GetObject :one
SELECT sqlc.embed(objects), c.name AS collection_name
FROM objects
JOIN collections c ON c.id = objects.collection_id
WHERE objects.tenant_id = $1 AND objects.id = $2;

-- name: GetObjectsByIDs :many
-- Batch lookup for batch-operation executors: one round-trip for the
-- whole id list instead of one GetObject per id (a 1000-object batch
-- used to issue 1000 sequential SELECTs before any state mutation).
SELECT sqlc.embed(objects), c.name AS collection_name
FROM objects
JOIN collections c ON c.id = objects.collection_id
WHERE objects.tenant_id = $1 AND objects.id = ANY($2::uuid[]);

-- name: LookupObjectByID :one
-- Reads an object by id alone. Used by background workers (reconciler,
-- replicator) that don't carry a tenant context. Joins collections to
-- materialize the bucket binding so the caller can call S3 in one trip.
SELECT o.id, o.tenant_id, o.path, o.state,
       c.name  AS collection_name,
       sb.name AS backend_name,
       bk.name AS bucket_name
FROM objects o
JOIN collections c       ON c.id = o.collection_id
JOIN buckets bk          ON bk.id = c.bucket_id
JOIN storage_backends sb ON sb.id = bk.backend_id
WHERE o.id = $1;

-- name: LookupObjectByKey :one
-- Used by resource-name resolution: collections/{b}/objects-by-key/{path} → id.
SELECT sqlc.embed(o), c.name AS collection_name
FROM objects o
JOIN collections c ON c.id = o.collection_id
WHERE o.tenant_id = $1
  AND c.name = $2
  AND o.path = $3
  AND o.state <> 'DELETED';

-- name: RestoreObject :execrows
-- Undeletes a soft-deleted object iff no live row exists with the same
-- (tenant, collection_id, path). Caller is expected to verify uniqueness first;
-- a UNIQUE partial index still catches the race at commit time.
UPDATE objects
SET state         = 'AVAILABLE',
    terminated_at = NULL
WHERE tenant_id = $1 AND id = $2
  AND state = 'DELETED';

-- name: GetObjectLockState :one
-- Lock state for one object, so the delete handler can return a clear
-- "locked" error instead of a bare version-mismatch when the SQL guard
-- on HardDeleteObject zeroes the rowcount.
--
-- The lock belongs to the VERSION now (ADR-0013), so this reads through the
-- object's current version. LEFT JOIN plus COALESCE: an object with no lock
-- row is the common case and must answer "not locked", not "no row".
SELECT l.mode AS lock_mode, l.retain_until AS lock_retain_until,
       COALESCE(l.legal_hold, false) AS legal_hold
FROM objects o
LEFT JOIN object_locks l ON l.version_id = o.current_version_id
WHERE o.tenant_id = $1 AND o.id = $2;

-- name: HardDeleteObject :execrows
-- Removes the row outright. Caller is responsible for first deleting the
-- object from the storage backend (S3 DeleteObject). Allowed from any
-- state. expected_version=0 skips the OCC guard.
--
-- Object-lock guard mirrors enforce_object_version_lock() on
-- object_versions (the trigger only covers that table, NOT objects).
-- legal_hold and active COMPLIANCE locks are absolute; an active
-- GOVERNANCE lock is honoured unless the session sets
-- paladin.governance_bypass=true (HardDeleteWithBypass does, the plain RPC
-- path does not). A locked row matches 0 rows here, so the caller must
-- pre-check to distinguish "locked" from "version mismatch".
DELETE FROM objects
WHERE objects.tenant_id = $1 AND objects.id = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint)
  -- The lock lives on the object's current version now (ADR-0013). NOT EXISTS
  -- rather than a join: an object with no lock row is the common case and must
  -- remain deletable.
  AND NOT EXISTS (
      SELECT 1 FROM object_locks l
       WHERE l.version_id = objects.current_version_id
         AND (l.legal_hold
              OR (l.mode = 'COMPLIANCE' AND l.retain_until > now())
              OR (l.mode = 'GOVERNANCE' AND l.retain_until > now()
                  AND NOT COALESCE(
                      current_setting('paladin.governance_bypass', true)::boolean,
                      false))));

-- name: ListHardDeletable :many
-- Picks DELETED objects past the cooling-off window for the
-- LifecycleHardDeleter worker.
--
-- Returns the backend and bucket by NAME, not by id: the storage layer
-- addresses S3 by name, so resolving ids here saves the worker a lookup per
-- row — and makes it impossible to hand a uuid to a DELETE that wanted a name.
-- The collection's NAME comes back for the same reason: it is a segment of the
-- object's storage path (<tenant>/<collection>/<path>), not a lookup key.
--
-- Locks live on object_locks keyed by the object's CURRENT version, so the
-- guard is a LEFT JOIN. legal_hold and any active retention window block the
-- purge; the worker has no governance bypass, so GOVERNANCE is honoured here
-- exactly like COMPLIANCE. Locked rows are skipped until the lock lapses.
-- COALESCE on legal_hold because most objects have no lock row at all.
SELECT o.id, o.tenant_id, o.path, o.resource_version,
       sb.name AS backend_name,
       b.name  AS bucket_name,
       k.name  AS collection_name
FROM objects o
JOIN collections k     ON k.id = o.collection_id
JOIN buckets b         ON b.id = k.bucket_id
JOIN storage_backends sb ON sb.id = b.backend_id
LEFT JOIN object_locks l ON l.version_id = o.current_version_id
WHERE o.state = 'DELETED'
  AND o.terminated_at IS NOT NULL
  AND o.terminated_at < $1
  AND NOT COALESCE(l.legal_hold, false)
  AND NOT (l.retain_until IS NOT NULL AND l.retain_until > now())
ORDER BY o.terminated_at
LIMIT sqlc.arg('batch_size');

-- name: HardDeleteObjectIfStillDeleted :execrows
-- Defence-in-depth variant of HardDeleteObject for the worker path.
-- Re-asserts state='DELETED' AND resource_version=$2 in the WHERE
-- clause so a concurrent Restore (DELETED → AVAILABLE bumps
-- resource_version via the trigger) makes the worker's DELETE a
-- no-op. Worker callers pass the version they read from
-- ListHardDeletable; mismatch ⇒ 0 rows affected ⇒ skip.
DELETE FROM objects
WHERE objects.id = $1
  AND objects.state = 'DELETED'
  AND objects.resource_version = sqlc.arg('expected_version')::bigint
  -- Same lock guard as ListHardDeletable: a lock applied after the row
  -- was listed but before the worker deletes still blocks the purge.
  -- The worker has no governance bypass, so both modes block equally here.
  AND NOT EXISTS (
      SELECT 1 FROM object_locks l
       WHERE l.version_id = objects.current_version_id
         AND (l.legal_hold OR l.retain_until > now()));

-- name: CheckLiveCollision :one
-- True when a non-DELETED row already exists at (tenant, collection_id, path).
-- Used by RestoreObject to refuse restoring into a slot that's been reused.
SELECT EXISTS(
    SELECT 1 FROM objects o
    JOIN collections c ON c.id = o.collection_id
    WHERE o.tenant_id = $1
      AND c.name = $2
      AND o.path = $3
      AND o.state <> 'DELETED'
)::boolean AS exists;

-- name: ListObjects :many
-- The full CEL filter is still applied by the caller post-load; the
-- `state` / `prefix` / `substr` nargs are PUSHDOWN narrowing hints
-- extracted from that CEL (cel.ExtractObjectPushdown) so the DB drops
-- non-matching rows before they cross the wire instead of fetching the
-- whole namespace and filtering in Go. The post-load CEL pass stays
-- authoritative, so over-fetching (a hint that's absent) only costs
-- throughput, never correctness. `substr` is escaped for LIKE by the
-- adapter. Keyset page uses id (UUIDv7) which is monotonic-by-time.
SELECT sqlc.embed(o), c.name AS collection_name
FROM objects o
JOIN collections c ON c.id = o.collection_id
WHERE o.tenant_id = $1
  AND c.name = $2
  AND (sqlc.narg('state')::object_state IS NULL OR o.state = sqlc.narg('state')::object_state)
  AND (sqlc.narg('prefix')::text IS NULL OR o.path LIKE sqlc.narg('prefix')::text || '%')
  AND (sqlc.narg('substr')::text IS NULL
       OR o.path LIKE '%' || sqlc.narg('substr')::text || '%')
  AND (sqlc.narg('after_id')::uuid IS NULL OR o.id > sqlc.narg('after_id')::uuid)
ORDER BY o.id
LIMIT sqlc.arg('page_size');

-- name: CountObjects :one
SELECT COUNT(*) AS n
FROM objects o
JOIN collections c ON c.id = o.collection_id
WHERE o.tenant_id = $1
  AND c.name = $2
  AND (sqlc.narg('state')::object_state IS NULL
       OR o.state = sqlc.narg('state')::object_state);

-- name: ScanPendingExpired :many
-- Reconciler picks up PENDING rows whose presign has expired.
SELECT sqlc.embed(objects), c.name AS collection_name
FROM objects
JOIN collections c ON c.id = objects.collection_id
WHERE state = 'PENDING'
  AND presign_expires_at < now()
ORDER BY presign_expires_at
LIMIT sqlc.arg('batch_size');

-- name: UpdateObjectMetadata :execrows
UPDATE objects
SET metadata     = COALESCE(sqlc.narg('metadata'), metadata),
    tags         = COALESCE(sqlc.narg('tags'),     tags),
    external_ref = COALESCE(sqlc.narg('external_ref'), external_ref)
WHERE tenant_id = $1 AND id = $2
  AND state = 'AVAILABLE'
  AND resource_version = sqlc.arg('expected_version');
