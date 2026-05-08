-- Lifecycle worker queries.

-- name: ListBucketsWithReplication :many
-- Returns buckets that have replication.enabled = true. Used by the
-- replication worker to drive its fan-out scan; same row shape as the
-- lifecycle source so the decode helper is shared.
--
-- provision_state filter: only 'ready' buckets are valid worker targets.
-- 'pending' rows have no physical bucket yet, 'deleting' rows are on
-- their way out, and 'failed' / 'deletion_failed' need operator triage —
-- replicating into or out of any of those is at best wasted work and at
-- worst ships objects into a bucket that's about to be torn down.
SELECT backend_id, bucket_name, display_name, region, labels,
       owner_tenant_id, cedar_policy, cedar_policy_hash, constraints,
       lifecycle_rules,
       object_lock_enabled, object_lock_default_mode, object_lock_default_retention_seconds,
       versioning_enabled, versioning_keep_deletes_forever,
       replication_enabled, replication_destination, replication_filter,
       provision_state,
       resource_version, created_at, updated_at
FROM buckets
WHERE replication_enabled = TRUE
  AND replication_destination <> ''
  AND provision_state = 'ready'
ORDER BY backend_id, bucket_name;

-- name: ListBucketsWithLifecycle :many
-- Returns only buckets with a non-empty lifecycle_rules array. The worker
-- ticks against this set; sweeping all buckets on every tick would be
-- wasteful when most carry no rules.
--
-- See ListBucketsWithReplication for why we restrict to provision_state='ready'.
SELECT backend_id, bucket_name, display_name, region, labels,
       owner_tenant_id, cedar_policy, cedar_policy_hash, constraints,
       lifecycle_rules,
       object_lock_enabled, object_lock_default_mode, object_lock_default_retention_seconds,
       versioning_enabled, versioning_keep_deletes_forever,
       replication_enabled, replication_destination, replication_filter,
       provision_state,
       resource_version, created_at, updated_at
FROM buckets
WHERE jsonb_array_length(lifecycle_rules) > 0
  AND provision_state = 'ready'
ORDER BY backend_id, bucket_name;

-- name: ListObjectKeyBindingsForBucket :many
-- Lists every (tenant_id, object_key) bound to a given bucket. Used by
-- lifecycle + replication workers to scope their object scans.
SELECT tenant_id, object_key
FROM object_keys
WHERE backend_id = $1 AND bucket_name = $2
ORDER BY tenant_id, object_key;

-- name: IterateObjectsForLifecycle :many
-- Streams a window of AVAILABLE-only objects under (tenant, object_key)
-- newest-first. Pagination cursor: object_id (UUIDv7 → time-ordered).
-- Lifecycle worker walks via repeated calls until empty page.
SELECT object_id, state, content_type, size_bytes,
       metadata, tags, created_at, committed_at
FROM objects
WHERE tenant_id = $1
  AND object_key = $2
  AND state = 'AVAILABLE'
  AND ($3::uuid IS NULL OR object_id < $3::uuid)
ORDER BY object_id DESC
LIMIT $4;
