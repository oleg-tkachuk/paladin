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
--
-- bucket_id, enabled and read_only, with provision_state, are what the bucket
-- gate decides the source's read on (bucketgate.State).
SELECT sb.name AS backend_name,
       b.name AS bucket_name, b.display_name, b.region, b.labels,
       b.owner_tenant_id, b.cedar_policy, b.cedar_policy_hash, b.constraints,
       b.lifecycle_rules,
       b.object_lock_enabled, b.object_lock_default_mode, b.object_lock_default_retention_seconds,
       b.versioning_enabled, b.versioning_keep_deletes_forever,
       b.replication_enabled, b.replication_destination, b.replication_filter,
       b.provision_state, b.public_read, b.public_base_url, b.created_on_backend,
       b.resource_version, b.created_at, b.updated_at,
       b.id AS bucket_id, sb.enabled, sb.read_only
FROM buckets b
JOIN storage_backends sb ON sb.id = b.backend_id
WHERE b.replication_enabled = TRUE
  AND b.replication_destination <> ''
  AND b.provision_state = 'ready'
ORDER BY b.name;

-- name: GetBucketGateState :one
-- The bucket gate's state of one bucket, by backend and bucket name: what the
-- replication worker decides a write to a replication destination on.
SELECT b.id AS bucket_id, sb.enabled, sb.read_only, b.provision_state
FROM buckets b
JOIN storage_backends sb ON sb.id = b.backend_id
WHERE sb.name = $1 AND b.name = $2;

-- name: ListBucketsWithLifecycle :many
-- Returns only buckets with a non-empty lifecycle_rules array. The worker
-- ticks against this set; sweeping all buckets on every tick would be
-- wasteful when most carry no rules.
--
-- See ListBucketsWithReplication for why we restrict to provision_state='ready'.
--
-- bucket_id, enabled and read_only, with provision_state, are what the bucket
-- gate decides the expiry on (bucketgate.State): the worker asks it in Go and
-- skips a bucket it refuses for the tick.
SELECT sb.name AS backend_name,
       b.name AS bucket_name, b.display_name, b.region, b.labels,
       b.owner_tenant_id, b.cedar_policy, b.cedar_policy_hash, b.constraints,
       b.lifecycle_rules,
       b.object_lock_enabled, b.object_lock_default_mode, b.object_lock_default_retention_seconds,
       b.versioning_enabled, b.versioning_keep_deletes_forever,
       b.replication_enabled, b.replication_destination, b.replication_filter,
       b.provision_state, b.public_read, b.public_base_url, b.created_on_backend,
       b.resource_version, b.created_at, b.updated_at,
       b.id AS bucket_id, sb.enabled, sb.read_only
FROM buckets b
JOIN storage_backends sb ON sb.id = b.backend_id
WHERE jsonb_array_length(b.lifecycle_rules) > 0
  AND b.provision_state = 'ready'
ORDER BY b.name;

-- name: ListCollectionBindingsForBucket :many
-- Lists every (tenant_id, collection name) bound to a given bucket. Used by
-- lifecycle + replication workers to scope their object scans.
-- A tenant in the trash is frozen: its rows wait for a restore or a purge.
SELECT c.tenant_id, c.name AS collection_name
FROM collections c
JOIN buckets b           ON b.id = c.bucket_id
JOIN storage_backends sb ON sb.id = b.backend_id
WHERE sb.name = $1 AND b.name = $2
  AND NOT EXISTS (SELECT 1 FROM tenants t WHERE t.id = c.tenant_id AND t.deleted_at IS NOT NULL)
ORDER BY c.tenant_id, c.name;

-- name: IterateObjectsForLifecycle :many
-- Streams a window of AVAILABLE-only objects under (tenant, collection)
-- newest-first. Pagination cursor: id (UUIDv7 → time-ordered).
-- Lifecycle worker walks via repeated calls until empty page.
-- Every column cel.ObjectVars projects, so a rule's match sees what a
-- ListObjects filter sees.
SELECT o.id, o.path, o.state, o.content_type, o.size_bytes, o.external_ref,
       o.metadata, o.tags, o.created_at, o.updated_at, o.committed_at,
       o.resource_version
FROM objects o
WHERE o.tenant_id = $1
  AND o.collection_id = (SELECT c.id FROM collections c
                          WHERE c.tenant_id = $1 AND c.name = $2)
  AND o.state = 'AVAILABLE'
  AND ($3::uuid IS NULL OR o.id < $3::uuid)
ORDER BY o.id DESC
LIMIT $4;
