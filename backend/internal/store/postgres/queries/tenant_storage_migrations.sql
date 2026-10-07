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
WHERE m.tenant_id = $1;

-- name: ListActiveStorageMigrations :many
-- Worker scan: non-terminal migrations, oldest-touched first. 'completed' is
-- still active — the worker must run retention-gated cleanup on it.
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

-- name: SetStorageMigrationState :execrows
UPDATE tenant_storage_migrations
SET state = sqlc.arg('to_state'), error = '', updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state');

-- name: SetStorageMigrationTotal :execrows
-- Records the object count and moves provisioning -> copying.
UPDATE tenant_storage_migrations
SET objects_total = sqlc.arg('objects_total'), state = sqlc.arg('to_state'), updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state');

-- name: AdvanceStorageMigrationCopy :execrows
-- Records copy progress + the resume cursor after a batch. Progress only moves
-- forward: a stale batch reporting less than is recorded changes nothing.
UPDATE tenant_storage_migrations
SET objects_copied    = sqlc.arg('objects_copied'),
    cursor_collection = sqlc.arg('cursor_collection'),
    cursor_path       = sqlc.arg('cursor_path'),
    updated_at        = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state')
  AND objects_copied <= sqlc.arg('objects_copied');

-- name: CompleteStorageMigration :execrows
-- Rebind verified: serve from the dedicated bucket. The old copies are kept
-- until cleanup_after (now + the row's retention) so a bad migration is still
-- rollback-able within the window.
UPDATE tenant_storage_migrations
SET state = sqlc.arg('to_state'), error = '', completed_at = now(),
    cleanup_after = now() + make_interval(secs => cleanup_retention_seconds),
    updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state');

-- name: MarkStorageMigrationCleaned :execrows
UPDATE tenant_storage_migrations
SET state = sqlc.arg('to_state'), cleaned_at = now(), updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state');

-- name: FailStorageMigration :execrows
UPDATE tenant_storage_migrations
SET state = sqlc.arg('to_state'), error = sqlc.arg('error'), attempts = attempts + 1, updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND state = sqlc.arg('from_state');

-- name: MigrationListTenantObjects :many
-- Objects to copy, keyset-paginated by (collection_id, path) after the cursor so a
-- worker restart resumes mid-prefix instead of rescanning from the top.
-- size_bytes feeds the physical (HEAD size) verify after copy.
SELECT c.name AS collection_name, o.path, COALESCE(o.size_bytes, 0)::bigint AS size_bytes
FROM objects o
JOIN collections c ON c.id = o.collection_id
WHERE o.tenant_id = $1
  AND o.state = 'AVAILABLE'
  AND ROW(c.name, o.path) > ROW(sqlc.arg('after_collection')::text,
                                sqlc.arg('after_path')::text)
ORDER BY c.name, o.path
LIMIT sqlc.arg('limit_count')::int;

-- name: MigrationCountTenantObjects :one
SELECT count(*) FROM objects WHERE tenant_id = $1 AND state = 'AVAILABLE';

-- name: SetTenantStorageLayout :execrows
UPDATE tenants
SET storage_layout   = $2,
    resource_version = resource_version + 1,
    updated_at       = now()
WHERE id = $1;

-- name: MigrationListTenantCollections :many
-- All collections of a tenant, for the transactional rebind.
SELECT name FROM collections WHERE tenant_id = $1 ORDER BY name;

-- name: TenantCollectionBuckets :many
-- Distinct buckets the tenant's collections currently bind to. The migration
-- copies FROM this — a shared tenant's collections normally share one bucket;
-- more than one row means the tenant spans buckets (not supported in slice 1).
SELECT DISTINCT sb.name AS backend_name, b.name AS bucket_name
FROM collections c
JOIN buckets b           ON b.id = c.bucket_id
JOIN storage_backends sb ON sb.id = b.backend_id
WHERE c.tenant_id = $1;
