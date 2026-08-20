-- Object queries.

-- name: CreateObject :exec
INSERT INTO objects (
    id, tenant_id, collection_id, path, state,
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
WHERE tenant_id = $1 AND id = $2;

-- name: GetObjectsByIDs :many
-- Batch lookup for batch-operation executors: one round-trip for the
-- whole id list instead of one GetObject per id (a 1000-object batch
-- used to issue 1000 sequential SELECTs before any state mutation).
SELECT sqlc.embed(objects)
FROM objects
WHERE tenant_id = $1 AND id = ANY($2::uuid[]);

-- name: LookupObjectByID :one
-- Reads an object by id alone. Used by background workers (reconciler,
-- replicator) that don't carry a tenant context. Joins collections to
-- materialize the bucket binding so the caller can call S3 in one trip.
SELECT o.id, o.tenant_id, o.collection_id, o.path, o.state,
       b.bucket_id
FROM objects o
JOIN collections b
  ON b.id = o.collection_id
WHERE o.id = $1;

-- name: LookupObjectByKey :one
-- Used by resource-name resolution: collections/{b}/objects-by-key/{path} → id.
SELECT sqlc.embed(objects)
FROM objects
WHERE tenant_id = $1 AND collection_id = $2 AND path = $3 AND state <> 'DELETED';

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
WHERE id = $1
  AND state IN ('PENDING', 'AVAILABLE')
  AND (sqlc.narg('sequencer')::text IS NULL
       OR sequencer IS NULL
       OR sqlc.narg('sequencer')::text > sequencer);

-- name: MarkObjectFailed :execrows
UPDATE objects
SET state         = 'FAILED',
    terminated_at = now()
WHERE id = $1
  AND state = 'PENDING';

-- name: SoftDeleteObject :execrows
-- expected_version=0 disables the OCC guard (force).
UPDATE objects
SET state         = 'DELETED',
    terminated_at = now()
WHERE tenant_id = $1 AND id = $2
  AND state IN ('AVAILABLE', 'PENDING')
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

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
WHERE tenant_id = $1 AND id = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint)
  AND NOT legal_hold
  AND NOT (lock_mode = 'COMPLIANCE' AND lock_retain_until IS NOT NULL
           AND lock_retain_until > now())
  AND NOT (lock_mode = 'GOVERNANCE' AND lock_retain_until IS NOT NULL
           AND lock_retain_until > now()
           AND NOT COALESCE(current_setting('paladin.governance_bypass', true)::boolean, false));

-- name: ListHardDeletable :many
-- Picks DELETED objects past the cooling-off window for the
-- LifecycleHardDeleter worker. Joins collections to materialise
-- (backend_id, bucket_name) so the worker issues the storage DELETE
-- in one round-trip per row without a second lookup.
-- Bounded at the caller's batch_size; the worker loops on the
-- ticker to drain a backlog without holding a single statement open.
SELECT o.id, o.tenant_id, o.collection_id, o.path, o.resource_version,
       k.bucket_id
FROM objects o
JOIN collections k
  ON k.tenant_id = o.tenant_id AND k.collection_id = o.collection_id
WHERE o.state = 'DELETED'
  AND o.terminated_at IS NOT NULL
  AND o.terminated_at < $1
  -- Never purge a locked object: legal hold or an active COMPLIANCE /
  -- GOVERNANCE retention window. The worker has no governance-bypass,
  -- so governance locks are honoured here too. Locked rows are simply
  -- skipped until the lock lapses, then become eligible normally.
  AND NOT o.legal_hold
  AND NOT (o.lock_mode = 'COMPLIANCE' AND o.lock_retain_until IS NOT NULL
           AND o.lock_retain_until > now())
  AND NOT (o.lock_mode = 'GOVERNANCE' AND o.lock_retain_until IS NOT NULL
           AND o.lock_retain_until > now())
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
WHERE id = $1
  AND state = 'DELETED'
  AND resource_version = sqlc.arg('expected_version')::bigint
  -- Same lock guard as ListHardDeletable: a lock applied after the row
  -- was listed but before the worker deletes still blocks the purge.
  AND NOT legal_hold
  AND NOT (lock_mode = 'COMPLIANCE' AND lock_retain_until IS NOT NULL
           AND lock_retain_until > now())
  AND NOT (lock_mode = 'GOVERNANCE' AND lock_retain_until IS NOT NULL
           AND lock_retain_until > now());

-- name: CheckLiveCollision :one
-- True when a non-DELETED row already exists at (tenant, collection_id, path).
-- Used by RestoreObject to refuse restoring into a slot that's been reused.
SELECT EXISTS(
    SELECT 1 FROM objects
    WHERE tenant_id = $1
      AND collection_id = $2
      AND path = $3
      AND state <> 'DELETED'
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
SELECT sqlc.embed(objects)
FROM objects
WHERE tenant_id = $1
  AND collection_id = $2
  AND (sqlc.narg('state')::object_state IS NULL OR state = sqlc.narg('state')::object_state)
  AND (sqlc.narg('prefix')::text IS NULL OR path LIKE sqlc.narg('prefix')::text || '%')
  AND (sqlc.narg('substr')::text IS NULL OR path LIKE '%' || sqlc.narg('substr')::text || '%')
  AND (sqlc.narg('after_id')::uuid IS NULL OR id > sqlc.narg('after_id')::uuid)
ORDER BY id
LIMIT sqlc.arg('page_size');

-- name: CountObjects :one
SELECT COUNT(*) AS n
FROM objects
WHERE tenant_id = $1 AND collection_id = $2
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
WHERE tenant_id = $1 AND id = $2
  AND state = 'AVAILABLE'
  AND resource_version = sqlc.arg('expected_version');
