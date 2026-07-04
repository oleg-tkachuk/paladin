-- ADR-0011 Phase 3: shared->dedicated storage migration copy job.

-- name: CreateStorageMigration :one
INSERT INTO tenant_storage_migrations
    (tenant_id, source_backend_id, source_bucket_name, target_backend_id, target_bucket_name, state)
VALUES ($1, $2, $3, $4, $5, 'provisioning')
RETURNING *;

-- name: GetStorageMigration :one
SELECT * FROM tenant_storage_migrations WHERE tenant_id = $1;

-- name: ListActiveStorageMigrations :many
-- Worker scan: non-terminal migrations, oldest-touched first.
SELECT * FROM tenant_storage_migrations
WHERE state NOT IN ('completed', 'failed')
ORDER BY updated_at
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
    cursor_object_key = $3,
    cursor_key        = $4,
    updated_at        = now()
WHERE tenant_id = $1;

-- name: CompleteStorageMigration :execrows
UPDATE tenant_storage_migrations
SET state = 'completed', error = '', completed_at = now(), updated_at = now()
WHERE tenant_id = $1;

-- name: FailStorageMigration :execrows
UPDATE tenant_storage_migrations
SET state = 'failed', error = $2, attempts = attempts + 1, updated_at = now()
WHERE tenant_id = $1;

-- name: MigrationListTenantObjects :many
-- Objects to copy, keyset-paginated by (object_key, key) after the cursor so a
-- worker restart resumes mid-prefix instead of rescanning from the top.
SELECT object_key, key
FROM objects
WHERE tenant_id = $1
  AND state = 'AVAILABLE'
  AND ROW(object_key, key) > ROW(sqlc.arg('after_object_key')::text, sqlc.arg('after_key')::text)
ORDER BY object_key, key
LIMIT sqlc.arg('limit_count')::int;

-- name: MigrationCountTenantObjects :one
SELECT count(*) FROM objects WHERE tenant_id = $1 AND state = 'AVAILABLE';

-- name: SetTenantStorageLayout :execrows
UPDATE tenants
SET storage_layout   = $2,
    resource_version = resource_version + 1,
    updated_at       = now()
WHERE tenant_id = $1;

-- name: MigrationListTenantObjectKeys :many
-- All object_keys of a tenant, for the transactional rebind.
SELECT object_key FROM object_keys WHERE tenant_id = $1 ORDER BY object_key;
