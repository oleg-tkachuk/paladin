-- ADR-0011 Phase 3: shared->dedicated storage migration copy job.

-- name: CreateStorageMigration :one
INSERT INTO tenant_storage_migrations
    (tenant_id, source_bucket_id, target_bucket_id,
     cleanup_retention_seconds, state)
VALUES ($1, $2, $3, $4, 'provisioning')
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
ORDER BY m.updated_at
LIMIT sqlc.arg('limit_count')::int;

-- name: SetStorageMigrationState :execrows
UPDATE tenant_storage_migrations
SET state = $2, error = '', updated_at = now()
WHERE tenant_id = $1;

-- name: SetStorageMigrationTotal :execrows
-- Records the object count and moves provisioning -> copying.
UPDATE tenant_storage_migrations
SET objects_total = $2, state = 'copying', updated_at = now()
WHERE tenant_id = $1;

-- name: AdvanceStorageMigrationCopy :execrows
-- Records copy progress + the resume cursor after a batch.
UPDATE tenant_storage_migrations
SET objects_copied    = $2,
    cursor_collection = $3,
    cursor_path        = $4,
    updated_at        = now()
WHERE tenant_id = $1;

-- name: CompleteStorageMigration :execrows
-- Rebind verified: serve from the dedicated bucket. The old copies are kept
-- until cleanup_after (now + the row's retention) so a bad migration is still
-- rollback-able within the window.
UPDATE tenant_storage_migrations
SET state = 'completed', error = '', completed_at = now(),
    cleanup_after = now() + make_interval(secs => cleanup_retention_seconds),
    updated_at = now()
WHERE tenant_id = $1;

-- name: MarkStorageMigrationCleaned :execrows
UPDATE tenant_storage_migrations
SET state = 'cleaned', cleaned_at = now(), updated_at = now()
WHERE tenant_id = $1;

-- name: FailStorageMigration :execrows
UPDATE tenant_storage_migrations
SET state = 'failed', error = $2, attempts = attempts + 1, updated_at = now()
WHERE tenant_id = $1;

-- name: MigrationListTenantObjects :many
-- Objects to copy, keyset-paginated by (collection_id, path) after the cursor so a
-- worker restart resumes mid-prefix instead of rescanning from the top.
-- size_bytes feeds the physical (HEAD size) verify after copy.
SELECT collection_id, path, COALESCE(size_bytes, 0)::bigint AS size_bytes
FROM objects
WHERE tenant_id = $1
  AND state = 'AVAILABLE'
  AND ROW(collection, key) > ROW(sqlc.arg('after_collection')::text, sqlc.arg('after_key')::text)
ORDER BY collection_id, path
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
SELECT DISTINCT bucket_id FROM collections WHERE tenant_id = $1;
