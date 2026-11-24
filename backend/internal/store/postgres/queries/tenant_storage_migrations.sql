-- A tenant's storage migration (ADR-0015, ADR-0051): the tenant's objects
-- move one by one from its shared bucket into a dedicated one.

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
-- still active — the worker must clean the source on it — and so is
-- 'rolling_back', which the worker must finish rolling back.
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

-- Every transition below applies only to the migration as the worker read
-- it: the same state AND the same attempts. attempts grows on every failure
-- and a retry keeps it, so a worker that read an earlier attempt and resumed
-- after it failed and was retried matches nothing (ABA on state alone).

-- name: LockStorageMigrationAsRead :execrows
-- Holds the migration row for a transaction that changes where the tenant's
-- uploads go, and only while it is still as the worker read it.
SELECT 1 FROM tenant_storage_migrations
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state')
  AND attempts = sqlc.arg('attempts')
FOR UPDATE;

-- name: HoldStorageMigrationAsRead :execrows
-- Holds the migration row shared for one object's move, while it is still as
-- the worker read it: a stale worker moves nothing, and a transition waits
-- for the moves in flight.
SELECT 1 FROM tenant_storage_migrations
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state')
  AND attempts = sqlc.arg('attempts')
FOR SHARE;

-- name: SetStorageMigrationState :execrows
UPDATE tenant_storage_migrations
SET state = sqlc.arg('to_state'), error = '', updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state')
  AND attempts = sqlc.arg('attempts');

-- name: MigrationListBoundCollections :many
-- The tenant's private collections bound to a bucket: what a pass points at
-- the bucket the tenant's uploads go to. Public collections never move
-- (collections_public_bucket_fixed, ADR-0027).
SELECT name FROM collections
WHERE tenant_id = sqlc.arg('tenant_id') AND bucket_id = sqlc.arg('bucket_id') AND NOT public_read
ORDER BY name;

-- name: MigrationRepointDefaultBinding :execrows
-- Points the tenant's default binding at to_bucket_id when it names
-- from_bucket_id or the tenant has none, so a bare-name collection created
-- from here on lands there; an operator's choice of another bucket is left
-- alone.
INSERT INTO tenant_default_bindings (tenant_id, bucket_id, set_by)
VALUES (sqlc.arg('tenant_id'), sqlc.arg('to_bucket_id'), sqlc.arg('set_by'))
ON CONFLICT (tenant_id) DO UPDATE
   SET bucket_id = EXCLUDED.bucket_id, set_at = now(), set_by = EXCLUDED.set_by
 WHERE tenant_default_bindings.bucket_id = sqlc.arg('from_bucket_id');

-- name: MigrationCloseWrites :execrows
-- Records when the last URL that can write the bucket being emptied expires
-- (writes_close_at, ADR-0051), once per direction: no URL for it is signed
-- after this pass, and none signed before outlives limits.presign.max_ttl.
UPDATE tenant_storage_migrations
SET writes_close_at = COALESCE(writes_close_at, now() + sqlc.arg('window')::interval),
    updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state')
  AND attempts = sqlc.arg('attempts');

-- name: SetTenantStorageLayout :execrows
UPDATE tenants
SET storage_layout   = $2,
    resource_version = resource_version + 1,
    updated_at       = now()
WHERE id = $1;

-- name: MigrationListMovable :many
-- One batch of a pass: the tenant's live and trashed objects in private
-- collections placed in from_bucket_id whose upload anchor has settled — no
-- URL can write their key there any more (ADR-0051) — after the pass's
-- cursor, by id. A PENDING row is placed nowhere and is not listed; its
-- promote places it, and a later pass moves it.
SELECT o.id, c.name AS collection_name, o.path,
       COALESCE(o.size_bytes, 0)::bigint AS size_bytes,
       COALESCE(o.etag, '')::text AS etag,
       (o.state = 'DELETED')::bool AS trashed,
       o.bytes_written_at
FROM objects o
JOIN collections c ON c.id = o.collection_id
WHERE o.tenant_id = sqlc.arg('tenant_id')
  AND o.placement_bucket_id = sqlc.arg('from_bucket_id')
  AND o.upload_bucket_id IS NULL
  AND o.state IN ('AVAILABLE', 'DELETED')
  AND NOT c.public_read
  -- No cursor (NULL) starts the pass at the first id.
  AND (sqlc.narg('after_id')::uuid IS NULL OR o.id > sqlc.narg('after_id')::uuid)
ORDER BY o.id
LIMIT sqlc.arg('limit_count')::int;

-- name: SetIdleInTransactionTimeout :one
-- Sets idle_in_transaction_session_timeout for the rest of the transaction
-- (set_config's is_local), for one held open across a copy (MoveObject).
SELECT set_config('idle_in_transaction_session_timeout', sqlc.arg('timeout')::text, true);

-- name: MigrationHoldMovable :one
-- Holds one object's row against a delete for its move (FOR KEY SHARE: a tag
-- or a taint still goes through), and reads what the move is decided on. No
-- row: purged since it was listed, or no longer placed in from_bucket_id, or
-- anchored again — nothing to move.
SELECT COALESCE(o.size_bytes, 0)::bigint AS size_bytes,
       COALESCE(o.etag, '')::text AS etag,
       (o.state = 'DELETED')::bool AS trashed,
       o.bytes_written_at
FROM objects o
WHERE o.id = sqlc.arg('id') AND o.tenant_id = sqlc.arg('tenant_id')
  AND o.placement_bucket_id = sqlc.arg('from_bucket_id')
  AND o.upload_bucket_id IS NULL
  AND o.state IN ('AVAILABLE', 'DELETED')
FOR KEY SHARE OF o;

-- name: MigrationPlaceMoved :execrows
-- The move's compare-and-set: the object is read from to_bucket_id from here
-- on, with the ETag that bucket reported, only while it is still placed in
-- from_bucket_id, settled, and its bytes are the ones the copy read — its
-- content version (bytes_written_at) unchanged.
UPDATE objects
SET placement_bucket_id = sqlc.arg('to_bucket_id'),
    etag = COALESCE(NULLIF(sqlc.arg('etag')::text, ''), etag)
WHERE id = sqlc.arg('id') AND tenant_id = sqlc.arg('tenant_id')
  AND placement_bucket_id = sqlc.arg('from_bucket_id')
  AND upload_bucket_id IS NULL
  AND bytes_written_at IS NOT DISTINCT FROM sqlc.narg('bytes_written_at');

-- name: CountStorageMigrationMove :execrows
UPDATE tenant_storage_migrations
SET objects_copied = objects_copied + 1, updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state')
  AND attempts = sqlc.arg('attempts');

-- name: MigrationHolds :one
-- What keeps the bucket being emptied (from_bucket_id) in use, for the
-- migration's completion (ADR-0051): the tenant's objects placed there, its
-- rows anchored there (a URL signed for it may still write), its multipart
-- sessions there, its private collections and default binding still bound
-- there — and whether the last URL signed for it has expired by the grace.
SELECT
    (SELECT count(*) FROM objects o JOIN collections c ON c.id = o.collection_id
      WHERE o.tenant_id = sqlc.arg('tenant_id') AND o.placement_bucket_id = sqlc.arg('from_bucket_id')
        AND NOT c.public_read)::bigint AS placed,
    (SELECT count(*) FROM objects o
      WHERE o.tenant_id = sqlc.arg('tenant_id') AND o.upload_bucket_id = sqlc.arg('from_bucket_id'))::bigint AS anchored,
    (SELECT count(*) FROM multipart_uploads mu
      WHERE mu.tenant_id = sqlc.arg('tenant_id') AND mu.bucket_id = sqlc.arg('from_bucket_id'))::bigint AS sessions,
    ((SELECT count(*) FROM collections c
       WHERE c.tenant_id = sqlc.arg('tenant_id') AND c.bucket_id = sqlc.arg('from_bucket_id') AND NOT c.public_read)
     + (SELECT count(*) FROM tenant_default_bindings d
         WHERE d.tenant_id = sqlc.arg('tenant_id') AND d.bucket_id = sqlc.arg('from_bucket_id')))::bigint AS bound,
    (m.writes_close_at IS NOT NULL
     AND now() >= m.writes_close_at + sqlc.arg('grace')::interval)::bool AS writes_closed
FROM tenant_storage_migrations m
WHERE m.tenant_id = sqlc.arg('tenant_id') AND m.state = sqlc.arg('from_state')
  AND m.attempts = sqlc.arg('attempts');

-- name: RecordStorageMigrationPass :one
-- A pass ended: counts the passes in a row whose moves failed their
-- verification (none resets it), and the objects the migration covers —
-- moved, and still placed in the bucket being emptied.
UPDATE tenant_storage_migrations
SET failed_passes = CASE WHEN sqlc.arg('failed')::bool THEN failed_passes + 1 ELSE 0 END,
    objects_total = objects_copied + sqlc.arg('remaining')::bigint,
    updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state')
  AND attempts = sqlc.arg('attempts')
RETURNING failed_passes;

-- name: CompleteStorageMigration :execrows
-- Every object moved and no URL left that can write the source: the source
-- copies are kept until cleanup_after — the retention the operator asked for,
-- and at least the longest a GET URL signed for the source before its object
-- moved can live (get_window).
UPDATE tenant_storage_migrations
SET state = sqlc.arg('to_state'), error = '', completed_at = now(),
    cleanup_after = now() + GREATEST(make_interval(secs => cleanup_retention_seconds),
                                     sqlc.arg('get_window')::interval),
    cursor_collection = '', cursor_path = '',
    updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state')
  AND attempts = sqlc.arg('attempts');

-- name: MigrationListCleanable :many
-- The cleanup's walk: the tenant's live and trashed objects in private
-- collections placed in the target, after the cursor by (collection, path).
-- Each one's source copy is deleted; an object that never had one costs a
-- delete of an absent key, which succeeds.
SELECT c.name AS collection_name, o.path
FROM objects o
JOIN collections c ON c.id = o.collection_id
WHERE o.tenant_id = sqlc.arg('tenant_id')
  AND o.placement_bucket_id = sqlc.arg('target_bucket_id')
  AND NOT c.public_read
  AND ROW(c.name, o.path) > ROW(sqlc.arg('after_collection')::text, sqlc.arg('after_path')::text)
ORDER BY c.name, o.path
LIMIT sqlc.arg('limit_count')::int;

-- name: AdvanceStorageMigrationCleanup :execrows
UPDATE tenant_storage_migrations
SET cursor_collection = sqlc.arg('cursor_collection'),
    cursor_path       = sqlc.arg('cursor_path'),
    updated_at        = now()
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

-- name: StartStorageMigrationRollback :execrows
-- moving -> rolling_back, recording the reason and counting the failure. The
-- moves start over in the other direction: their count, the failed passes
-- and the write deadline, which the rollback's first pass sets for the
-- target.
UPDATE tenant_storage_migrations
SET state = sqlc.arg('to_state'), error = sqlc.arg('error'), attempts = attempts + 1,
    objects_copied = 0, failed_passes = 0, writes_close_at = NULL,
    updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state')
  AND attempts = sqlc.arg('attempts');

-- name: FinishStorageMigrationRollback :execrows
-- rolling_back -> failed, once nothing of the tenant is left in the target
-- and no URL can write it. The reason the rollback started stays, and the
-- attempt too: the failure was counted when it started.
UPDATE tenant_storage_migrations
SET state = sqlc.arg('to_state'), updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state')
  AND attempts = sqlc.arg('attempts');

-- name: UploadWritesCloseAt :one
-- The deadline a URL signed for the bucket (backend_name, bucket_name) must
-- expire by: the bucket is being emptied by the tenant's migration — its
-- source while moving, its target while rolling back — whose passes have set
-- the deadline. No row: nothing bounds the URL but its TTL.
SELECT m.writes_close_at::timestamptz AS writes_close_at
FROM tenant_storage_migrations m
JOIN buckets b           ON b.id = CASE WHEN m.state = 'rolling_back'
                                        THEN m.target_bucket_id ELSE m.source_bucket_id END
JOIN storage_backends sb ON sb.id = b.backend_id
WHERE m.tenant_id = sqlc.arg('tenant_id')
  AND m.state IN ('moving', 'rolling_back')
  AND m.writes_close_at IS NOT NULL
  AND sb.name = sqlc.arg('backend_name') AND b.name = sqlc.arg('bucket_name');

-- name: TenantCollectionBuckets :many
-- Distinct buckets the tenant's collections currently bind to. The migration
-- moves objects FROM this — a shared tenant's collections normally share one
-- bucket; more than one row means the tenant spans buckets (not supported).
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
    failed_passes             = 0,
    writes_close_at           = NULL,
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
    writes_close_at    = NULL,
    failed_passes      = 0,
    updated_at         = now()
FROM tenant_default_bindings d
WHERE m.id = sqlc.arg('id') AND m.state = 'failed'
  AND d.tenant_id = m.tenant_id AND d.bucket_id = m.target_bucket_id
RETURNING m.*;
