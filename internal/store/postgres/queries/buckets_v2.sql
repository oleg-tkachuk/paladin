-- v2 bucket queries — full surface for admin/v1.BucketService.

-- name: GetBucketV2 :one
SELECT backend_id, bucket_name, display_name, region, labels,
       owner_tenant_id, cedar_policy, cedar_policy_hash, constraints,
       lifecycle_rules,
       object_lock_enabled, object_lock_default_mode, object_lock_default_retention_seconds,
       versioning_enabled, versioning_keep_deletes_forever,
       replication_enabled, replication_destination, replication_filter,
       provision_state,
       resource_version, created_at, updated_at
FROM buckets
WHERE backend_id = $1 AND bucket_name = $2;

-- name: CreateBucketV2 :exec
INSERT INTO buckets (
    backend_id, bucket_name, display_name, region, labels,
    owner_tenant_id, cedar_policy, constraints,
    provision_state
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: ListPendingBucketProvisions :many
-- Worker query: drag the next batch of buckets that need a backend
-- CreateBucket call. ORDER BY last_provision_at NULLS FIRST so brand-new
-- rows are picked up before failed-and-waiting-for-retry rows. Caller is
-- expected to apply its own backoff before recalling on failed rows.
SELECT backend_id, bucket_name, region, provision_state,
       provision_attempts, last_provision_at
FROM buckets
WHERE provision_state = 'pending'
   OR (provision_state = 'failed' AND provision_attempts < sqlc.arg('max_attempts')::int)
ORDER BY last_provision_at NULLS FIRST, backend_id, bucket_name
LIMIT sqlc.arg('limit_count')::int;

-- name: MarkBucketProvisionReady :execrows
UPDATE buckets
SET provision_state    = 'ready',
    provision_error    = '',
    provision_attempts = provision_attempts + 1,
    last_provision_at  = now()
WHERE backend_id = $1 AND bucket_name = $2;

-- name: MarkBucketProvisionFailed :execrows
-- terminal=true → the worker hit a non-retryable error (auth denied,
-- region mismatch, …) and the row should stop receiving attempts.
-- terminal=false → transient error; row stays 'pending' and gets
-- retried on the next tick after the configured backoff.
UPDATE buckets
SET provision_state    = CASE WHEN sqlc.arg('terminal')::bool THEN 'failed' ELSE 'pending' END,
    provision_error    = sqlc.arg('err_msg')::text,
    provision_attempts = provision_attempts + 1,
    last_provision_at  = now()
WHERE backend_id = $1 AND bucket_name = $2;

-- name: ListBucketsV2 :many
SELECT backend_id, bucket_name, display_name, region, labels,
       owner_tenant_id, cedar_policy, cedar_policy_hash, constraints,
       lifecycle_rules,
       object_lock_enabled, object_lock_default_mode, object_lock_default_retention_seconds,
       versioning_enabled, versioning_keep_deletes_forever,
       replication_enabled, replication_destination, replication_filter,
       provision_state,
       resource_version, created_at, updated_at
FROM buckets
WHERE (sqlc.narg('backend_id')::text IS NULL OR backend_id = sqlc.narg('backend_id')::text)
  AND (backend_id, bucket_name) > (sqlc.arg('after_backend_id')::text, sqlc.arg('after_name')::text)
ORDER BY backend_id, bucket_name
LIMIT sqlc.arg('page_size');

-- name: ListAccessibleBuckets :many
-- Returns shared buckets (owner IS NULL) plus buckets owned by the tenant.
SELECT backend_id, bucket_name, display_name, region, labels,
       owner_tenant_id, cedar_policy, cedar_policy_hash, constraints,
       lifecycle_rules,
       object_lock_enabled, object_lock_default_mode, object_lock_default_retention_seconds,
       versioning_enabled, versioning_keep_deletes_forever,
       replication_enabled, replication_destination, replication_filter,
       provision_state,
       resource_version, created_at, updated_at
FROM buckets
WHERE (owner_tenant_id IS NULL OR owner_tenant_id = $1)
  AND (backend_id, bucket_name) > ($2::text, $3::text)
ORDER BY backend_id, bucket_name
LIMIT $4;

-- name: SetBucketPolicy :execrows
UPDATE buckets
SET cedar_policy      = $3,
    cedar_policy_hash = NULL
WHERE backend_id = $1 AND bucket_name = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: SetBucketLifecycle :execrows
UPDATE buckets
SET lifecycle_rules = $3
WHERE backend_id = $1 AND bucket_name = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: SetBucketObjectLock :execrows
UPDATE buckets
SET object_lock_enabled                   = $3,
    object_lock_default_mode              = $4,
    object_lock_default_retention_seconds = $5
WHERE backend_id = $1 AND bucket_name = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: SetBucketVersioning :execrows
UPDATE buckets
SET versioning_enabled                = $3,
    versioning_keep_deletes_forever   = $4
WHERE backend_id = $1 AND bucket_name = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: SetBucketReplication :execrows
UPDATE buckets
SET replication_enabled     = $3,
    replication_destination = $4,
    replication_filter      = $5
WHERE backend_id = $1 AND bucket_name = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: SetBucketConstraints :execrows
UPDATE buckets
SET constraints = $3
WHERE backend_id = $1 AND bucket_name = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: UpdateBucketBasic :execrows
UPDATE buckets
SET display_name = COALESCE(sqlc.narg('display_name'), display_name),
    labels       = COALESCE(sqlc.narg('labels'), labels),
    owner_tenant_id = COALESCE(sqlc.narg('owner_tenant_id'), owner_tenant_id)
WHERE backend_id = $1 AND bucket_name = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: DeleteBucketV2 :execrows
-- Physical row delete. Called by the bucket-reconciler worker AFTER
-- s3.DeleteBucket confirms. The handler does NOT call this directly —
-- it flips state to 'deleting' via MarkBucketDeleting and lets the
-- worker drive the physical delete.
DELETE FROM buckets
WHERE backend_id = $1 AND bucket_name = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: MarkBucketDeleting :execrows
-- Soft-flip into the deletion outbox. Resets attempts so the new
-- operation gets a fresh retry budget; clears any old error message.
-- The actual DELETE happens later, from the worker, after backend
-- confirmation.
UPDATE buckets
SET provision_state    = 'deleting',
    provision_error    = '',
    provision_attempts = 0,
    last_provision_at  = NULL
WHERE backend_id = $1 AND bucket_name = $2
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: ListPendingBucketDeletions :many
-- Worker query for the delete path. Picks 'deleting' rows plus
-- 'deletion_failed' rows whose retry budget hasn't run out.
SELECT backend_id, bucket_name, region, provision_state,
       provision_attempts, last_provision_at
FROM buckets
WHERE provision_state = 'deleting'
   OR (provision_state = 'deletion_failed'
       AND provision_attempts < sqlc.arg('max_attempts')::int)
ORDER BY last_provision_at NULLS FIRST, backend_id, bucket_name
LIMIT sqlc.arg('limit_count')::int;

-- name: MarkBucketDeletionFailed :execrows
-- Mirror of MarkBucketProvisionFailed for the delete side. terminal=true
-- parks the row in 'deletion_failed' (operator triage); terminal=false
-- keeps the row 'deleting' so the next tick retries.
UPDATE buckets
SET provision_state    = CASE WHEN sqlc.arg('terminal')::bool THEN 'deletion_failed' ELSE 'deleting' END,
    provision_error    = sqlc.arg('err_msg')::text,
    provision_attempts = provision_attempts + 1,
    last_provision_at  = now()
WHERE backend_id = $1 AND bucket_name = $2;
