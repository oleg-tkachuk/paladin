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
-- Records the object count, the copy's start and moves provisioning ->
-- copying. Run under the row's lock (LockStorageMigrationForCount), which
-- every promote holds while the migration provisions: the clock read here is
-- later than every promote that committed before, and earlier than the
-- stamp of every promote after (statemachine.checkPlacement).
UPDATE tenant_storage_migrations
SET objects_total = sqlc.arg('objects_total'), state = sqlc.arg('to_state'),
    copy_started_at = clock_timestamp(), updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state')
  AND attempts = sqlc.arg('attempts');

-- name: LockStorageMigrationForCount :execrows
-- Holds the migration row while the copy's total is counted, and only while
-- it is still as the worker read it. A purge's transaction updates the row
-- (ForgetPurgedFromStorageMigrationCount), so the count runs after any purge
-- that touched it has committed, and a purge after it waits for 'copying'.
SELECT 1 FROM tenant_storage_migrations
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state')
  AND attempts = sqlc.arg('attempts')
FOR UPDATE;

-- name: ForgetPurgedFromStorageMigrationCount :exec
-- A purge's part in the copy's count, on its transaction, after the row is
-- deleted. The copy fails when it ends short of the total it counted, which
-- an object purged before the copy reached it would cause though nothing was
-- missed: such a purge takes it off the total. Counted means the copy's list
-- holds it — live or trashed, not in a public collection — and not reached
-- means past the cursor, which the copy advances on the transaction that
-- holds the object's row (CopyObjectHeld), so a purge either waited for that
-- copy and sees it counted, or ran first and the copy finds no row.
-- In 'provisioning' it only takes the row's lock: the count is taken under
-- it (LockStorageMigrationForCount), after this purge is visible.
UPDATE tenant_storage_migrations
SET objects_total = objects_total - CASE
        WHEN state = 'copying'
         AND sqlc.arg('object_state')::text IN ('AVAILABLE', 'DELETED')
         AND NOT EXISTS (SELECT 1 FROM collections c
                          WHERE c.tenant_id = sqlc.arg('tenant_id')
                            AND c.name = sqlc.arg('collection_name')::text AND c.public_read)
         AND ROW(cursor_collection, cursor_path)
             < ROW(sqlc.arg('collection_name')::text, sqlc.arg('path')::text)
        THEN 1 ELSE 0 END
WHERE tenant_id = sqlc.arg('tenant_id') AND state IN ('provisioning', 'copying');

-- name: ForgetStorageMigrationCaughtUp :exec
-- Drops what an attempt's catch-up recorded (migration 116): at the start of
-- an attempt's copy, which copies everything again, and once it completes.
DELETE FROM storage_migration_caught_up WHERE tenant_id = $1;

-- name: MigrationListUncaughtUp :many
-- The objects the catch-up copies before the rebind: live or trashed, in a
-- private collection, whose bytes were written since the copy started and
-- whose latest write the catch-up has not copied (migration 116). The copy
-- listed each object once, behind a moving cursor; a write after its start
-- may have reached only the source. Only while the migration is as the
-- worker read it, so a stale worker lists nothing. The set shrinks as the
-- catch-up copies, and grows by what is written meanwhile, in any order: it
-- has no cursor for a late commit to fall behind.
SELECT o.id, o.bytes_written_at AS written_at,
       c.name AS collection_name, o.path,
       COALESCE(o.size_bytes, 0)::bigint AS size_bytes,
       (o.state = 'DELETED')::bool AS trashed
FROM objects o
JOIN collections c ON c.id = o.collection_id
JOIN tenant_storage_migrations m ON m.tenant_id = o.tenant_id
LEFT JOIN storage_migration_caught_up u ON u.tenant_id = o.tenant_id AND u.object_id = o.id
WHERE o.tenant_id = sqlc.arg('tenant_id')
  AND m.state = sqlc.arg('from_state') AND m.attempts = sqlc.arg('attempts')
  AND o.state IN ('AVAILABLE', 'DELETED')
  AND NOT c.public_read
  AND o.bytes_written_at >= COALESCE(m.copy_started_at, m.created_at)
  AND u.written_at IS DISTINCT FROM o.bytes_written_at
ORDER BY o.bytes_written_at, o.id
LIMIT sqlc.arg('limit_count')::int;

-- name: MigrationCountUncaughtUp :one
-- How many objects MigrationListUncaughtUp would still return. The rebind
-- counts them under the migration row's lock, which every promote holds
-- while it runs, so no write commits unseen between the count and the
-- rebind: any is the rebind refused.
SELECT count(*)
FROM objects o
JOIN collections c ON c.id = o.collection_id
JOIN tenant_storage_migrations m ON m.tenant_id = o.tenant_id
LEFT JOIN storage_migration_caught_up u ON u.tenant_id = o.tenant_id AND u.object_id = o.id
WHERE o.tenant_id = sqlc.arg('tenant_id')
  AND m.state = sqlc.arg('from_state') AND m.attempts = sqlc.arg('attempts')
  AND o.state IN ('AVAILABLE', 'DELETED')
  AND NOT c.public_read
  AND o.bytes_written_at >= COALESCE(m.copy_started_at, m.created_at)
  AND u.written_at IS DISTINCT FROM o.bytes_written_at;

-- name: MigrationHoldObjectForCatchUp :one
-- Holds one object's row against a delete and a promote (FOR KEY SHARE, which
-- a promote's FOR UPDATE waits on) while the catch-up copies it, and reads
-- the write whose bytes the copy takes. No row: purged, or no longer live or
-- trashed; nothing to copy.
SELECT o.bytes_written_at
FROM objects o
JOIN collections c ON c.id = o.collection_id
WHERE o.tenant_id = sqlc.arg('tenant_id') AND o.id = sqlc.arg('object_id')
  AND o.state IN ('AVAILABLE', 'DELETED')
  AND NOT c.public_read
FOR KEY SHARE OF o;

-- name: RecordStorageMigrationCaughtUp :execrows
-- Records the catch-up's copy of an object: the write it took and the ETag
-- the target reported for it. Only while the migration is as the worker read
-- it: a stale worker's copy is not taken for the current attempt's.
INSERT INTO storage_migration_caught_up (tenant_id, object_id, written_at, etag)
SELECT sqlc.arg('tenant_id'), sqlc.arg('object_id'), sqlc.arg('written_at'), sqlc.narg('etag')
WHERE EXISTS (SELECT 1 FROM tenant_storage_migrations m
               WHERE m.tenant_id = sqlc.arg('tenant_id')
                 AND m.state = sqlc.arg('from_state') AND m.attempts = sqlc.arg('attempts'))
ON CONFLICT (tenant_id, object_id) DO UPDATE
SET written_at = EXCLUDED.written_at, etag = EXCLUDED.etag, copied_at = now();

-- name: SetIdleInTransactionTimeout :one
-- Sets idle_in_transaction_session_timeout for the rest of the transaction
-- (set_config's is_local), for one held open across a copy (HoldObject).
SELECT set_config('idle_in_transaction_session_timeout', sqlc.arg('timeout')::text, true);

-- name: MigrationHoldObject :many
-- Holds the object's rows at (collection, path) against a delete for the
-- transaction (FOR KEY SHARE: a tag or a taint still goes through), so a
-- purge cannot remove the row while its bytes are copied: it waits, and then
-- records a debt for the copy. No row: the object was purged since it was
-- listed, and there is nothing to copy.
SELECT o.id
FROM objects o
JOIN collections c ON c.id = o.collection_id
WHERE o.tenant_id = $1
  AND c.name = sqlc.arg('collection_name')::text AND o.path = sqlc.arg('path')::text
  AND o.state IN ('AVAILABLE', 'DELETED')
  AND NOT c.public_read
FOR KEY SHARE OF o;

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
--
-- Live and trashed objects: a trashed object keeps its bytes until the purge,
-- and RestoreObject reads them from the bucket its collection is bound to, so
-- they move with the collection. PENDING rows do not: no bytes are confirmed
-- under them, and the upload URL they were handed names the source bucket,
-- which the row records (upload_bucket_id): their completion moves the bytes.
-- Versions and delete markers are rows only, all under the object's one key,
-- so they need nothing copied.
--
-- bound_* is the bucket the collection is bound to now: cleanup deletes a
-- source copy only of an object read from the target. written_at and
-- caught_up_* are when its bytes were last written and what the catch-up
-- copied of them (migration 116), for the verify.
SELECT c.name AS collection_name, o.path, COALESCE(o.size_bytes, 0)::bigint AS size_bytes,
       (o.state = 'DELETED')::bool AS trashed,
       sb.name AS bound_backend_name, b.name AS bound_bucket_name,
       o.bytes_written_at AS written_at,
       u.written_at AS caught_up_written_at, u.etag AS caught_up_etag
FROM objects o
JOIN collections c       ON c.id = o.collection_id
JOIN buckets b           ON b.id = c.bucket_id
JOIN storage_backends sb ON sb.id = b.backend_id
LEFT JOIN storage_migration_caught_up u ON u.tenant_id = o.tenant_id AND u.object_id = o.id
WHERE o.tenant_id = $1
  AND o.state IN ('AVAILABLE', 'DELETED')
  AND NOT c.public_read
  AND ROW(c.name, o.path) > ROW(sqlc.arg('after_collection')::text,
                                sqlc.arg('after_path')::text)
ORDER BY c.name, o.path
LIMIT sqlc.arg('limit_count')::int;

-- name: MigrationCountUploadsAnchoredTo :one
-- The tenant's uploads whose URL names the bucket (migration 113) and that
-- are still PENDING: their bytes may yet land there, and CompleteObject or
-- the reconciler move them from it. Cleanup of the source waits for none.
SELECT count(*)
FROM objects o
JOIN buckets b           ON b.id = o.upload_bucket_id
JOIN storage_backends sb ON sb.id = b.backend_id
WHERE o.tenant_id = $1 AND o.state = 'PENDING'
  AND sb.name = sqlc.arg('backend_name') AND b.name = sqlc.arg('bucket_name');

-- name: MigrationCountTenantObjects :one
-- The objects MigrationListTenantObjects walks: live and trashed, public
-- collections' excluded.
SELECT count(*)
FROM objects o
JOIN collections c ON c.id = o.collection_id
WHERE o.tenant_id = $1 AND o.state IN ('AVAILABLE', 'DELETED') AND NOT c.public_read;

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

-- name: LockStorageMigrationStart :exec
-- Waits for every promote of the tenant in flight, and holds back every
-- later one, until the transaction ends: each promote holds this lock shared
-- before it reads the migration's row (migstate.LockSpaceStart). Taken by a
-- transition into a fencing state from a non-fencing one, so every promote
-- either committed before it or reads the row it leaves.
SELECT pg_advisory_xact_lock(sqlc.arg('lock_space')::int, sqlc.arg('lock_key')::int);

-- name: LockTenantBindings :exec
-- Held by the rebind and the rebind back before they list the tenant's
-- collections (migstate.LockSpaceBindings): they wait for every collection
-- being created, list it, and hold back every later creation until they
-- commit.
SELECT pg_advisory_xact_lock(sqlc.arg('lock_space')::int, sqlc.arg('lock_key')::int);

-- name: LockTenantBindingsShared :exec
-- Held by a collection's creation: creations do not wait on each other, and
-- a rebind waits for them (LockTenantBindings).
SELECT pg_advisory_xact_lock_shared(sqlc.arg('lock_space')::int, sqlc.arg('lock_key')::int);

-- name: BucketMigratedAway :one
-- Whether the tenant's latest storage migration moved its private
-- collections away from the bucket and nothing will move a collection bound
-- there back: rebound to the target, the source left behind
-- (migstate.SourceLeft); or rolled back, the target left behind. Asked under
-- LockTenantBindingsShared, so a rebind either committed before it or waits
-- for the creation asking.
SELECT EXISTS (
    SELECT 1
    FROM (SELECT * FROM tenant_storage_migrations
           WHERE tenant_id = sqlc.arg('tenant_id')
           ORDER BY created_at DESC
           LIMIT 1) m
    JOIN buckets b           ON b.id = CASE WHEN m.state = sqlc.arg('rolled_back_state')::text
                                            THEN m.target_bucket_id ELSE m.source_bucket_id END
    JOIN storage_backends sb ON sb.id = b.backend_id
    WHERE sb.name = sqlc.arg('backend_name') AND b.name = sqlc.arg('bucket_name')
      AND m.rebound_at IS NOT NULL
      AND (m.state = ANY(sqlc.arg('source_left_states')::text[])
           OR m.state = sqlc.arg('rolled_back_state')::text)
)::bool;

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
-- it through a collection, a default binding or a grant. Run with the cross-tenant
-- read on, or RLS would hide exactly the rows being looked for.
SELECT b.id, b.owner_tenant_id, b.public_read, b.provision_state,
       (EXISTS (SELECT 1 FROM collections c
                 WHERE c.bucket_id = b.id AND c.tenant_id <> sqlc.arg('tenant_id'))
        OR EXISTS (SELECT 1 FROM tenant_default_bindings d
                    WHERE d.bucket_id = b.id AND d.tenant_id <> sqlc.arg('tenant_id'))
        OR EXISTS (SELECT 1 FROM bucket_grants g
                    WHERE g.bucket_id = b.id AND g.tenant_id <> sqlc.arg('tenant_id')))::bool
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
    rebound_back_at           = NULL,
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
-- tenant rebound. Every object written into the dedicated bucket has a
-- bytes_written_at at or after it.
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

-- name: ForgetStorageMigrationCopiedBack :exec
-- Drops what a rollback recorded (migration 118): when a rollback starts,
-- and once it has rebound the tenant back.
DELETE FROM storage_migration_copied_back WHERE tenant_id = $1;

-- name: MigrationListUncopiedBack :many
-- The objects the rollback copies back: live or trashed (a trashed object's
-- bytes are what a later RestoreObject reads), in a private collection
-- (public ones never moved, ADR-0027), whose bytes were written at or after
-- the rebind and whose latest write the rollback has not copied back
-- (migration 118). written_at is when the object's bytes were last written
-- (bytes_written_at, which every promote sets), not when its row last
-- changed: a tag, a taint, a lock or a trash move after the rebind leaves
-- its bytes where they were, and its only target copy may be the broken one
-- the verify failed on. A row promoted before the column existed has none;
-- its updated_at stands in, at or after its last write. Only while the
-- migration is as the worker read it. The set has no cursor for a late
-- commit to fall behind: a promote stamped before a batch and committed
-- after it is listed by the next.
SELECT o.id, COALESCE(o.bytes_written_at, o.updated_at)::timestamptz AS written_at,
       c.name AS collection_name, o.path,
       COALESCE(o.size_bytes, 0)::bigint AS size_bytes
FROM objects o
JOIN collections c ON c.id = o.collection_id
JOIN tenant_storage_migrations m ON m.tenant_id = o.tenant_id
LEFT JOIN storage_migration_copied_back r ON r.tenant_id = o.tenant_id AND r.object_id = o.id
WHERE o.tenant_id = sqlc.arg('tenant_id')
  AND m.state = sqlc.arg('from_state') AND m.attempts = sqlc.arg('attempts')
  AND o.state IN ('AVAILABLE', 'DELETED')
  AND NOT c.public_read
  AND COALESCE(o.bytes_written_at, o.updated_at) >= m.rebound_at
  AND r.written_at IS DISTINCT FROM COALESCE(o.bytes_written_at, o.updated_at)
ORDER BY COALESCE(o.bytes_written_at, o.updated_at), o.id
LIMIT sqlc.arg('limit_count')::int;

-- name: MigrationCountUncopiedBack :one
-- How many objects MigrationListUncopiedBack would still return. The rebind
-- back counts them under the migration row's lock, which every promote holds
-- while the migration rolls back, so no write commits unseen between the
-- count and the rebind back: any is the rebind back refused.
SELECT count(*)
FROM objects o
JOIN collections c ON c.id = o.collection_id
JOIN tenant_storage_migrations m ON m.tenant_id = o.tenant_id
LEFT JOIN storage_migration_copied_back r ON r.tenant_id = o.tenant_id AND r.object_id = o.id
WHERE o.tenant_id = sqlc.arg('tenant_id')
  AND m.state = sqlc.arg('from_state') AND m.attempts = sqlc.arg('attempts')
  AND o.state IN ('AVAILABLE', 'DELETED')
  AND NOT c.public_read
  AND COALESCE(o.bytes_written_at, o.updated_at) >= m.rebound_at
  AND r.written_at IS DISTINCT FROM COALESCE(o.bytes_written_at, o.updated_at);

-- name: MigrationHoldObjectForCopyBack :one
-- Holds one object's row against a delete and a promote (FOR KEY SHARE,
-- which a promote's FOR UPDATE waits on) while the rollback copies it back,
-- and reads the write whose bytes the copy takes. No row: purged, or no
-- longer live or trashed; nothing to copy.
SELECT COALESCE(o.bytes_written_at, o.updated_at)::timestamptz AS written_at
FROM objects o
JOIN collections c ON c.id = o.collection_id
WHERE o.tenant_id = sqlc.arg('tenant_id') AND o.id = sqlc.arg('object_id')
  AND o.state IN ('AVAILABLE', 'DELETED')
  AND NOT c.public_read
FOR KEY SHARE OF o;

-- name: HoldStorageMigrationForCopyBack :execrows
-- Holds the migration row shared for a copy back, and only while it is
-- still as the worker read it: taken before the copy, after the object's
-- row, so a worker resumed past its lease finds out before it copies, and
-- the rebind back (FOR UPDATE) waits for every copy back in flight. Once the
-- tenant is on its source again, a stale copy from the target would put
-- older bytes over what was written there since.
SELECT 1 FROM tenant_storage_migrations
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state')
  AND attempts = sqlc.arg('attempts')
FOR SHARE;

-- name: RecordStorageMigrationCopiedBack :exec
-- Records the write a copy back took, or decided to leave, on the
-- transaction that holds the migration row as read.
INSERT INTO storage_migration_copied_back (tenant_id, object_id, written_at)
VALUES (sqlc.arg('tenant_id'), sqlc.arg('object_id'), sqlc.arg('written_at'))
ON CONFLICT (tenant_id, object_id) DO UPDATE
SET written_at = EXCLUDED.written_at, copied_at = now();

-- name: FinishStorageMigrationRollback :execrows
-- rolling_back -> failed, in the transaction that rebinds the tenant back.
-- The verify error stays, and attempts too: the failure was counted when the
-- rollback started. rebound_back_at is that transaction's now(), its START
-- time (migration 119): no later than any moment a writer could have seen
-- the tenant on its source again, so a write to the target that a late
-- storage event reports is older than any the row records from then on.
UPDATE tenant_storage_migrations
SET state = sqlc.arg('to_state'), rebound_back_at = now(), updated_at = now()
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
    rebound_back_at    = NULL,
    rollback_cursor_at = NULL,
    rollback_cursor_id = NULL,
    updated_at         = now()
FROM tenant_default_bindings d
WHERE m.id = sqlc.arg('id') AND m.state = 'failed'
  AND d.tenant_id = m.tenant_id AND d.bucket_id = m.target_bucket_id
RETURNING m.*;
