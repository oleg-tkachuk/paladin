-- v2 bucket queries — full surface for admin/v1.BucketService.

-- name: GetBucketV2 :one
SELECT (SELECT sb.name FROM storage_backends sb WHERE sb.id = buckets.backend_id) AS backend_name,
       name, display_name, region, labels,
       owner_tenant_id, cedar_policy, cedar_policy_hash, constraints,
       lifecycle_rules,
       object_lock_enabled, object_lock_default_mode, object_lock_default_retention_seconds,
       versioning_enabled, versioning_keep_deletes_forever,
       replication_enabled, replication_destination, replication_filter,
       provision_state,
       resource_version, created_at, updated_at
FROM buckets
WHERE id = (SELECT b.id FROM buckets b
              JOIN storage_backends sb ON sb.id = b.backend_id
             WHERE sb.name = $1 AND b.name = $2);

-- name: CreateBucketV2 :exec
INSERT INTO buckets (
    backend_id, name, display_name, region, labels,
    owner_tenant_id, cedar_policy, constraints,
    provision_state
) VALUES ((SELECT sb.id FROM storage_backends sb WHERE sb.name = $1),
        $2, $3, $4, $5, $6, $7, $8, $9);

-- name: ListPendingBucketProvisions :many
-- Worker query: drag the next batch of buckets that need a backend
-- CreateBucket call. ORDER BY last_provision_at NULLS FIRST so brand-new
-- rows are picked up before failed-and-waiting-for-retry rows. Caller is
-- expected to apply its own backoff before recalling on failed rows.
SELECT sb.name AS backend_name, b.name AS bucket_name, b.region, b.provision_state,
       b.provision_attempts, b.last_provision_at, b.owner_tenant_id
FROM buckets b
JOIN storage_backends sb ON sb.id = b.backend_id
WHERE b.provision_state = 'pending'
   OR (b.provision_state = 'failed' AND b.provision_attempts < sqlc.arg('max_attempts')::int)
ORDER BY b.last_provision_at NULLS FIRST, sb.name, b.name
LIMIT sqlc.arg('limit_count')::int;

-- name: MarkBucketProvisionReady :execrows
-- Only a row still being provisioned: a bucket marked for deletion while its
-- creation was in flight stays marked, rather than coming back as ready.
UPDATE buckets
SET provision_state    = 'ready',
    provision_error    = '',
    provision_attempts = provision_attempts + 1,
    last_provision_at  = now()
WHERE id = (SELECT b.id FROM buckets b
              JOIN storage_backends sb ON sb.id = b.backend_id
             WHERE sb.name = $1 AND b.name = $2)
  AND provision_state IN ('pending', 'failed');

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
WHERE id = (SELECT b.id FROM buckets b
              JOIN storage_backends sb ON sb.id = b.backend_id
             WHERE sb.name = $1 AND b.name = $2)
  AND provision_state IN ('pending', 'failed');

-- name: ListBucketsV2 :many
-- owner_tenant_id is an optional filter (nullable arg → skipped).
-- Index on buckets(owner_tenant_id) WHERE owner_tenant_id IS NOT NULL
-- (the schema baseline (001_initial_schema.sql)) makes the per-tenant filter cheap; the WHERE clause
-- below is plain equality so the planner uses the partial index.
SELECT sb.name AS backend_name,
       b.name, b.display_name, b.region, b.labels,
       b.owner_tenant_id, b.cedar_policy, b.cedar_policy_hash, b.constraints,
       b.lifecycle_rules,
       b.object_lock_enabled, b.object_lock_default_mode, b.object_lock_default_retention_seconds,
       b.versioning_enabled, b.versioning_keep_deletes_forever,
       b.replication_enabled, b.replication_destination, b.replication_filter,
       b.provision_state,
       b.resource_version, b.created_at, b.updated_at
FROM buckets b
JOIN storage_backends sb ON sb.id = b.backend_id
WHERE (sqlc.narg('backend_name')::text IS NULL OR sb.name = sqlc.narg('backend_name')::text)
  AND (sqlc.narg('owner_tenant_id')::uuid IS NULL OR b.owner_tenant_id = sqlc.narg('owner_tenant_id')::uuid)
  -- Pushdown hints from the caller's CEL filter (cel.ExtractPushdown).
  -- The full CEL program still runs over the fetched page, so a hint that is
  -- absent only widens the scan; see ListObjects for the contract.
  AND (sqlc.narg('name_eq')::text IS NULL OR b.name = sqlc.narg('name_eq')::text)
  AND (sqlc.narg('name_like')::text IS NULL OR b.name LIKE sqlc.narg('name_like')::text)
  AND (sqlc.narg('display_name_eq')::text IS NULL OR b.display_name = sqlc.narg('display_name_eq')::text)
  AND (sqlc.narg('display_name_like')::text IS NULL OR b.display_name LIKE sqlc.narg('display_name_like')::text)
  -- The derived `search` field, spelled to match cel.SearchText EXACTLY.
  -- ASCII-only folding via COLLATE "C" on both columns: Go's strings.ToLower
  -- and Postgres lower() are two Unicode implementations and may disagree, and
  -- a disagreement here drops a row the authoritative CEL pass accepts. See
  -- internal/filter/cel/searchtext.go.
  AND (sqlc.narg('search_like')::text IS NULL
       OR lower(b.name COLLATE "C") || chr(10)
          || lower(coalesce(b.display_name, '') COLLATE "C") || chr(10)
          || lower(sb.name COLLATE "C")
          LIKE sqlc.narg('search_like')::text)
  AND (sqlc.narg('versioning_enabled')::bool IS NULL OR b.versioning_enabled = sqlc.narg('versioning_enabled')::bool)
  AND (sqlc.narg('object_lock_enabled')::bool IS NULL OR b.object_lock_enabled = sqlc.narg('object_lock_enabled')::bool)
  AND (sqlc.narg('replication_enabled')::bool IS NULL OR b.replication_enabled = sqlc.narg('replication_enabled')::bool)
  -- Timestamp bounds. Strict `>` / `<` in the filter arrive here widened to
  -- their inclusive forms: the pushdown may only narrow, so an extra boundary
  -- row is free and a missing one is not.
  AND (sqlc.narg('created_at_gte')::timestamptz IS NULL
       OR b.created_at >= sqlc.narg('created_at_gte')::timestamptz)
  AND (sqlc.narg('created_at_lte')::timestamptz IS NULL
       OR b.created_at <= sqlc.narg('created_at_lte')::timestamptz)
  AND (sb.name, b.name) > (sqlc.arg('after_backend_id')::text, sqlc.arg('after_name')::text)
ORDER BY sb.name, b.name
LIMIT sqlc.arg('page_size');

-- name: ListAccessibleBuckets :many
-- Returns shared buckets (owner IS NULL) plus buckets owned by the tenant.
SELECT sb.name AS backend_name,
       b.name, b.display_name, b.region, b.labels,
       b.owner_tenant_id, b.cedar_policy, b.cedar_policy_hash, b.constraints,
       b.lifecycle_rules,
       b.object_lock_enabled, b.object_lock_default_mode, b.object_lock_default_retention_seconds,
       b.versioning_enabled, b.versioning_keep_deletes_forever,
       b.replication_enabled, b.replication_destination, b.replication_filter,
       b.provision_state,
       b.resource_version, b.created_at, b.updated_at
FROM buckets b
JOIN storage_backends sb ON sb.id = b.backend_id
WHERE (b.owner_tenant_id IS NULL OR b.owner_tenant_id = $1)
  AND (sb.name, b.name) > ($2::text, $3::text)
ORDER BY sb.name, b.name
LIMIT $4;

-- name: SetBucketPolicy :execrows
UPDATE buckets
SET cedar_policy      = $3,
    cedar_policy_hash = NULL
WHERE id = (SELECT b2.id FROM buckets b2
              JOIN storage_backends sb2 ON sb2.id = b2.backend_id
             WHERE sb2.name = $1 AND b2.name = $2)
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: SetBucketLifecycle :execrows
UPDATE buckets
SET lifecycle_rules = $3
WHERE id = (SELECT b2.id FROM buckets b2
              JOIN storage_backends sb2 ON sb2.id = b2.backend_id
             WHERE sb2.name = $1 AND b2.name = $2)
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: SetBucketObjectLock :execrows
UPDATE buckets
SET object_lock_enabled                   = $3,
    object_lock_default_mode              = $4,
    object_lock_default_retention_seconds = $5
WHERE id = (SELECT b2.id FROM buckets b2
              JOIN storage_backends sb2 ON sb2.id = b2.backend_id
             WHERE sb2.name = $1 AND b2.name = $2)
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: SetBucketVersioning :execrows
UPDATE buckets
SET versioning_enabled                = $3,
    versioning_keep_deletes_forever   = $4
WHERE id = (SELECT b2.id FROM buckets b2
              JOIN storage_backends sb2 ON sb2.id = b2.backend_id
             WHERE sb2.name = $1 AND b2.name = $2)
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: SetBucketReplication :execrows
UPDATE buckets
SET replication_enabled     = $3,
    replication_destination = $4,
    replication_filter      = $5
WHERE id = (SELECT b2.id FROM buckets b2
              JOIN storage_backends sb2 ON sb2.id = b2.backend_id
             WHERE sb2.name = $1 AND b2.name = $2)
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: SetBucketConstraints :execrows
UPDATE buckets
SET constraints = $3
WHERE id = (SELECT b2.id FROM buckets b2
              JOIN storage_backends sb2 ON sb2.id = b2.backend_id
             WHERE sb2.name = $1 AND b2.name = $2)
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: UpdateBucketBasic :execrows
UPDATE buckets
SET display_name = COALESCE(sqlc.narg('display_name'), display_name),
    labels       = COALESCE(sqlc.narg('labels'), labels),
    owner_tenant_id = COALESCE(sqlc.narg('owner_tenant_id'), owner_tenant_id)
WHERE id = (SELECT b2.id FROM buckets b2
              JOIN storage_backends sb2 ON sb2.id = b2.backend_id
             WHERE sb2.name = $1 AND b2.name = $2)
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: DeleteBucketV2 :execrows
-- Physical row delete. Called by the bucket-reconciler worker AFTER
-- s3.DeleteBucket confirms. The handler does NOT call this directly —
-- it flips state to 'deleting' via MarkBucketDeleting and lets the
-- worker drive the physical delete.
DELETE FROM buckets
WHERE id = (SELECT b2.id FROM buckets b2
              JOIN storage_backends sb2 ON sb2.id = b2.backend_id
             WHERE sb2.name = $1 AND b2.name = $2)
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
WHERE id = (SELECT b2.id FROM buckets b2
              JOIN storage_backends sb2 ON sb2.id = b2.backend_id
             WHERE sb2.name = $1 AND b2.name = $2)
  AND (sqlc.arg('expected_version')::bigint = 0
       OR resource_version = sqlc.arg('expected_version')::bigint);

-- name: ListPendingBucketDeletions :many
-- Worker query for the delete path. Picks 'deleting' rows plus
-- 'deletion_failed' rows whose retry budget hasn't run out.
SELECT sb.name AS backend_name, b.name AS bucket_name, b.region, b.provision_state,
       b.provision_attempts, b.last_provision_at
FROM buckets b
JOIN storage_backends sb ON sb.id = b.backend_id
WHERE b.provision_state = 'deleting'
   OR (b.provision_state = 'deletion_failed'
       AND b.provision_attempts < sqlc.arg('max_attempts')::int)
ORDER BY b.last_provision_at NULLS FIRST, sb.name, b.name
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
WHERE id = (SELECT b.id FROM buckets b
              JOIN storage_backends sb ON sb.id = b.backend_id
             WHERE sb.name = $1 AND b.name = $2)
  AND provision_state IN ('deleting', 'deletion_failed');
