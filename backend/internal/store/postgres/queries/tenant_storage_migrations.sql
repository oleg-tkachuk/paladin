-- ADR-0015 Phase 3: shared->dedicated storage migration copy job.

-- name: CreateStorageMigration :one
INSERT INTO tenant_storage_migrations
    (tenant_id, source_bucket_id, target_bucket_id,
     cleanup_retention_seconds, state)
SELECT $1, sb.id, tb.id, $6, 'provisioning'
FROM buckets sb
JOIN storage_backends ssb ON ssb.id = sb.backend_id
CROSS JOIN buckets tb
JOIN storage_backends tsb ON tsb.id = tb.backend_id
WHERE ssb.name = $2 AND sb.name = $3
  AND tsb.name = $4 AND tb.name = $5
RETURNING *;

-- name: GetStorageMigration :one
SELECT sqlc.embed(m),
       ssb.name AS source_backend_name, sb.name AS source_bucket_name,
       tsb.name AS target_backend_name, tb.name AS target_bucket_name
FROM tenant_storage_migrations m
JOIN buckets sb           ON sb.id = m.source_bucket_id
JOIN storage_backends ssb ON ssb.id = sb.backend_id
JOIN buckets tb           ON tb.id = m.target_bucket_id
JOIN storage_backends tsb ON tsb.id = tb.backend_id
WHERE m.tenant_id = $1
-- The latest, as StartStorageMigration reads it: a start used to be able to
-- leave a tenant with two rows (see LockTenantForStorageMigration).
ORDER BY m.created_at DESC
LIMIT 1;

-- name: ListActiveStorageMigrations :many
-- Worker scan: non-terminal migrations, oldest-touched first. 'completed' is
-- still active — the worker must run retention-gated cleanup on it — and so
-- is 'rolling_back', which the worker must finish rolling back.
SELECT sqlc.embed(m),
       ssb.name AS source_backend_name, sb.name AS source_bucket_name,
       tsb.name AS target_backend_name, tb.name AS target_bucket_name
FROM tenant_storage_migrations m
JOIN buckets sb           ON sb.id = m.source_bucket_id
JOIN storage_backends ssb ON ssb.id = sb.backend_id
JOIN buckets tb           ON tb.id = m.target_bucket_id
JOIN storage_backends tsb ON tsb.id = tb.backend_id
WHERE m.state NOT IN ('cleaned', 'failed')
  -- A tenant in the trash is frozen: its migration resumes on restore.
  AND NOT EXISTS (SELECT 1 FROM tenants t WHERE t.id = m.tenant_id AND t.deleted_at IS NOT NULL)
ORDER BY m.updated_at
LIMIT sqlc.arg('limit_count')::int;

-- Every transition names the state the worker read the migration in
-- (from_state) and applies only while it still holds: a worker resumed after a
-- pause past its lease must not drag a migration its successor moved on.

-- Every transition below applies only to the migration as the worker read
-- it: the same state AND the same attempts. attempts grows on every failure
-- and a retry keeps it, so a worker that read an earlier attempt and resumed
-- after it failed and was retried matches nothing (ABA on state alone).

-- name: SetStorageMigrationState :execrows
UPDATE tenant_storage_migrations
SET state = sqlc.arg('to_state'), error = '', updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state')
  AND attempts = sqlc.arg('attempts');

-- name: SetStorageMigrationTotal :execrows
-- Records the object count and moves provisioning -> copying.
UPDATE tenant_storage_migrations
SET objects_total = sqlc.arg('objects_total'), state = sqlc.arg('to_state'), updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state')
  AND attempts = sqlc.arg('attempts');

-- name: AdvanceStorageMigrationCopy :execrows
-- Records copy progress + the resume cursor after a batch. Progress only moves
-- forward: a stale batch reporting less than is recorded changes nothing.
UPDATE tenant_storage_migrations
SET objects_copied    = sqlc.arg('objects_copied'),
    cursor_collection = sqlc.arg('cursor_collection'),
    cursor_path       = sqlc.arg('cursor_path'),
    updated_at        = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state')
  AND attempts = sqlc.arg('attempts')
  AND objects_copied <= sqlc.arg('objects_copied');

-- name: CompleteStorageMigration :execrows
-- Rebind verified: serve from the dedicated bucket. The old copies are kept
-- until cleanup_after (now + the row's retention) so a bad migration is still
-- rollback-able within the window.
UPDATE tenant_storage_migrations
SET state = sqlc.arg('to_state'), error = '', completed_at = now(),
    cleanup_after = now() + make_interval(secs => cleanup_retention_seconds),
    updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state')
  AND attempts = sqlc.arg('attempts');

-- name: MarkStorageMigrationCleaned :execrows
UPDATE tenant_storage_migrations
SET state = sqlc.arg('to_state'), cleaned_at = now(), updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state')
  AND attempts = sqlc.arg('attempts');

-- name: FailStorageMigration :execrows
UPDATE tenant_storage_migrations
SET state = sqlc.arg('to_state'), error = sqlc.arg('error'), attempts = attempts + 1, updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state')
  AND attempts = sqlc.arg('attempts');

-- name: LockStorageMigrationForRebind :execrows
-- Holds the migration row for the rebind transaction, and only while it is
-- still in the state and attempt the worker read: a stale worker must not
-- repoint the tenant's collections at a bucket a newer attempt is still
-- filling.
SELECT 1 FROM tenant_storage_migrations
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state')
  AND attempts = sqlc.arg('attempts')
FOR UPDATE;

-- name: MigrationListTenantObjects :many
-- Objects to copy, keyset-paginated by (collection_id, path) after the cursor so a
-- worker restart resumes mid-prefix instead of rescanning from the top.
-- size_bytes feeds the physical (HEAD size) verify after copy. A public
-- collection's objects stay in their bucket (ADR-0027), so the copy, verify
-- and cleanup never see them.
SELECT c.name AS collection_name, o.path, COALESCE(o.size_bytes, 0)::bigint AS size_bytes
FROM objects o
JOIN collections c ON c.id = o.collection_id
WHERE o.tenant_id = $1
  AND o.state = 'AVAILABLE'
  AND NOT c.public_read
  AND ROW(c.name, o.path) > ROW(sqlc.arg('after_collection')::text,
                                sqlc.arg('after_path')::text)
ORDER BY c.name, o.path
LIMIT sqlc.arg('limit_count')::int;

-- name: MigrationCountTenantObjects :one
-- The objects MigrationListTenantObjects walks: public collections' excluded.
SELECT count(*)
FROM objects o
JOIN collections c ON c.id = o.collection_id
WHERE o.tenant_id = $1 AND o.state = 'AVAILABLE' AND NOT c.public_read;

-- name: SetTenantStorageLayout :execrows
UPDATE tenants
SET storage_layout   = $2,
    resource_version = resource_version + 1,
    updated_at       = now()
WHERE id = $1;

-- name: MigrationListTenantCollections :many
-- The collections the transactional rebind moves: all but the public ones,
-- which never rebind (collections_public_bucket_fixed, ADR-0027).
SELECT name FROM collections WHERE tenant_id = $1 AND NOT public_read ORDER BY name;

-- name: TenantCollectionBuckets :many
-- Distinct buckets the tenant's collections currently bind to. The migration
-- copies FROM this — a shared tenant's collections normally share one bucket;
-- more than one row means the tenant spans buckets (not supported in slice 1).
-- Public collections are left out: they bind to a public bucket of their own
-- and the migration leaves them there (ADR-0027).
SELECT DISTINCT sb.name AS backend_name, b.name AS bucket_name
FROM collections c
JOIN buckets b           ON b.id = c.bucket_id
JOIN storage_backends sb ON sb.id = b.backend_id
WHERE c.tenant_id = $1 AND NOT c.public_read;

-- Starting (or retrying) a migration: StartStorageMigration runs these in one
-- transaction, in this order, and says why each step is there.

-- name: LockTenantForStorageMigration :one
-- Serialises starts for one tenant. tenant_storage_migrations has no unique
-- key on tenant_id, so this lock is what keeps two concurrent starts (or
-- retries) from both going through. NO KEY UPDATE, not UPDATE: it does not
-- conflict with the KEY SHARE lock every insert of a row referencing the
-- tenant takes, so uploads are not held up behind it.
SELECT storage_layout FROM tenants
WHERE id = $1 AND deleted_at IS NULL
FOR NO KEY UPDATE;

-- name: LatestStorageMigration :one
SELECT id, state FROM tenant_storage_migrations
WHERE tenant_id = $1
ORDER BY created_at DESC
LIMIT 1;

-- name: EnsureStorageMigrationTargetBucket :exec
-- The tenant's dedicated bucket: pending, tenant-owned, private, created on
-- the backend by the reconciler. An existing row with that (backend, name) is
-- left as it is — ON CONFLICT DO NOTHING rather than catching the unique
-- violation, which aborts the transaction (25P02). Whether an existing row
-- may be used is StorageMigrationTargetBucket's question.
INSERT INTO buckets (backend_id, name, owner_tenant_id, provision_state,
                     public_read, created_on_backend)
SELECT sb.id, sqlc.arg('bucket_name'), sqlc.arg('owner_tenant_id'), 'pending', false, true
FROM storage_backends sb
WHERE sb.name = sqlc.arg('backend_name')
ON CONFLICT (backend_id, name) DO NOTHING;

-- name: StorageMigrationTargetBucket :one
-- What a start must know before it uses the target bucket: its owner, whether
-- it is public, its provisioning state, and whether any OTHER tenant reaches
-- it through a collection or a default binding. Run with the cross-tenant
-- read on, or RLS would hide exactly the rows being looked for.
SELECT b.id, b.owner_tenant_id, b.public_read, b.provision_state,
       (EXISTS (SELECT 1 FROM collections c
                 WHERE c.bucket_id = b.id AND c.tenant_id <> sqlc.arg('tenant_id'))
        OR EXISTS (SELECT 1 FROM tenant_default_bindings d
                    WHERE d.bucket_id = b.id AND d.tenant_id <> sqlc.arg('tenant_id')))::bool
       AS used_by_other_tenant
FROM buckets b
JOIN storage_backends sb ON sb.id = b.backend_id
WHERE sb.name = sqlc.arg('backend_name') AND b.name = sqlc.arg('bucket_name');

-- name: ResetFailedTargetBucketProvision :exec
-- A target bucket whose provisioning failed gets a fresh attempt budget, so a
-- retried migration does not wait on a reconciler that has given up on it.
UPDATE buckets
SET provision_state = 'pending', provision_error = '', provision_attempts = 0,
    updated_at = now()
WHERE id = $1 AND provision_state = 'failed';

-- name: RetryFailedStorageMigration :one
-- Starts a failed migration over from the beginning, in place: the row is the
-- tenant's migration history (attempts keeps counting failures), so it is
-- reset rather than deleted and re-inserted. Applies only while the row is
-- still 'failed' — the compare-and-set that, behind the tenant lock, lets
-- exactly one of two concurrent retries through.
UPDATE tenant_storage_migrations m
SET source_bucket_id          = sb.id,
    target_bucket_id          = tb.id,
    cleanup_retention_seconds = sqlc.arg('cleanup_retention_seconds'),
    state                     = 'provisioning',
    objects_total             = 0,
    objects_copied            = 0,
    cursor_collection         = '',
    cursor_path               = '',
    error                     = '',
    cleanup_after             = NULL,
    completed_at              = NULL,
    cleaned_at                = NULL,
    rebound_at                = NULL,
    rollback_cursor_at        = NULL,
    rollback_cursor_id        = NULL,
    updated_at                = now()
FROM buckets sb
JOIN storage_backends ssb ON ssb.id = sb.backend_id
CROSS JOIN buckets tb
JOIN storage_backends tsb ON tsb.id = tb.backend_id
WHERE m.id = sqlc.arg('id') AND m.state = 'failed'
  AND ssb.name = sqlc.arg('source_backend_name') AND sb.name = sqlc.arg('source_bucket_name')
  AND tsb.name = sqlc.arg('target_backend_name') AND tb.name = sqlc.arg('target_bucket_name')
RETURNING m.*;

-- Rolling a migration back (ADR-0015): a physical verify that fails after the
-- rebind moves the migration to 'rolling_back'; the worker copies the objects
-- written since the rebind back to the source bucket, then rebinds the tenant
-- to it and leaves the migration 'failed', retryable like any other failure.

-- name: MarkStorageMigrationRebound :execrows
-- Run in the rebind transaction: rebound_at is that transaction's now(), its
-- START time, so it is no later than any moment a writer could have seen the
-- tenant rebound. Every object written into the dedicated bucket has an
-- updated_at at or after it.
UPDATE tenant_storage_migrations
SET rebound_at = now(), updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state')
  AND attempts = sqlc.arg('attempts');

-- name: StartStorageMigrationRollback :execrows
-- verifying -> rolling_back. The verify failure is recorded and counted here,
-- where it happened; the rollback that follows keeps both. The cursor starts
-- empty: the worker begins at rebound_at.
UPDATE tenant_storage_migrations
SET state = sqlc.arg('to_state'), error = sqlc.arg('error'), attempts = attempts + 1,
    rollback_cursor_at = NULL, rollback_cursor_id = NULL, updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state')
  AND attempts = sqlc.arg('attempts');

-- name: MigrationListObjectsWrittenSince :many
-- The objects the rollback copies back: AVAILABLE, in a private collection
-- (public ones never moved, ADR-0027), and written at or after the cursor —
-- keyset on (updated_at, id), which the worker starts at (rebound_at, the
-- nil uuid). updated_at, not created_at: the bump_resource_version trigger
-- sets it on every UPDATE and the insert defaults it, so it covers a new
-- object, a promote, a storage-event overwrite and a restore alike. An object
-- written again after the cursor passed it moves back ahead of the cursor
-- and is copied again.
SELECT o.id, o.updated_at, c.name AS collection_name, o.path,
       COALESCE(o.size_bytes, 0)::bigint AS size_bytes
FROM objects o
JOIN collections c ON c.id = o.collection_id
WHERE o.tenant_id = sqlc.arg('tenant_id')
  AND o.state = 'AVAILABLE'
  AND NOT c.public_read
  AND ROW(o.updated_at, o.id) > ROW(sqlc.arg('after_at')::timestamptz,
                                    sqlc.arg('after_id')::uuid)
ORDER BY o.updated_at, o.id
LIMIT sqlc.arg('limit_count')::int;

-- name: MigrationObjectsWrittenSinceExist :one
-- Whether any object MigrationListObjectsWrittenSince would still return past
-- the cursor. The rebind back asks it under its lock: an object written since
-- the worker's last batch is still only in the dedicated bucket, and
-- rebinding before it is copied would lose it.
SELECT EXISTS (
    SELECT 1
    FROM objects o
    JOIN collections c ON c.id = o.collection_id
    WHERE o.tenant_id = sqlc.arg('tenant_id')
      AND o.state = 'AVAILABLE'
      AND NOT c.public_read
      AND ROW(o.updated_at, o.id) > ROW(sqlc.arg('after_at')::timestamptz,
                                        sqlc.arg('after_id')::uuid)
)::bool;

-- name: AdvanceStorageMigrationRollback :execrows
-- Records the rollback's resume cursor after a batch. It only moves forward:
-- a stale batch reporting an earlier cursor changes nothing.
UPDATE tenant_storage_migrations
SET rollback_cursor_at = sqlc.arg('cursor_at'),
    rollback_cursor_id = sqlc.arg('cursor_id'),
    updated_at         = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state')
  AND attempts = sqlc.arg('attempts')
  AND (rollback_cursor_at IS NULL
       OR ROW(rollback_cursor_at, rollback_cursor_id)
          <= ROW(sqlc.arg('cursor_at')::timestamptz, sqlc.arg('cursor_id')::uuid));

-- name: FinishStorageMigrationRollback :execrows
-- rolling_back -> failed, in the transaction that rebinds the tenant back.
-- The verify error stays, and attempts too: the failure was counted when the
-- rollback started.
UPDATE tenant_storage_migrations
SET state = sqlc.arg('to_state'), updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state')
  AND attempts = sqlc.arg('attempts');

-- name: RollBackStuckStorageMigration :one
-- A migration that failed its verify before rollbacks existed: 'failed', with
-- the tenant still on the dedicated bucket. Moves it to 'rolling_back' for
-- the worker, only while it is still 'failed' and the tenant's default
-- binding still names its target bucket — the tenant serves from where this
-- migration put it. Such a row has no rebound_at: the default binding's
-- set_at is the rebind transaction's now() when the migration set it (which
-- set_by records), and the migration's own creation is the fallback, earlier
-- than any rebind of it.
UPDATE tenant_storage_migrations m
SET state              = 'rolling_back',
    rebound_at         = COALESCE(m.rebound_at,
                                  CASE WHEN d.set_by = 'storage-migration' THEN d.set_at
                                       ELSE m.created_at END),
    rollback_cursor_at = NULL,
    rollback_cursor_id = NULL,
    updated_at         = now()
FROM tenant_default_bindings d
WHERE m.id = sqlc.arg('id') AND m.state = 'failed'
  AND d.tenant_id = m.tenant_id AND d.bucket_id = m.target_bucket_id
RETURNING m.*;
