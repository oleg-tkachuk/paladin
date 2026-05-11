-- +goose Up
-- +goose StatementBegin
-- 035_resource_name_canonical_backfill.sql
--
-- Phase 1 of canonical-resource-names: rewrite existing audit_log
-- `resource_name` values from the C-shape
--   tenants/{tenant_id}/objectKeys/{object_key}
-- to the canonical A-shape
--   storageBackends/{backend_id}/buckets/{bucket_name}/tenants/{tenant_id}/objectKeys/{object_key}
--
-- Only rows that match the C-shape ObjectKey prefix AND have a
-- corresponding row in object_keys are rewritten. Anything else
-- (tenant-rooted events, bucket-rooted events, free-form names) is
-- left untouched — those resources don't have a canonical form
-- different from their existing one.
--
-- The join is deterministic because (tenant_id, object_key) is UNIQUE
-- on object_keys and the C-shape carries both. Multi-segment object
-- keys (migration 030: `invoices/2026/q1`) work too because the
-- LIKE prefix matches the whole rest-of-name as the object_key.
--
-- event_deliveries: NOT touched. The resource_name lives inside the
-- `event_payload` JSONB column, not as a top-level column. Rewriting
-- it would require JSONB-rewrite and a re-validation pass; deliveries
-- are also short-lived (delivered/failed and then reaped within
-- minutes-hours), so the historical accuracy of resource_name there
-- isn't worth the migration complexity. New events ship canonical
-- from the same deploy that lands this migration.

-- Speed-up index on the audit_log column we're scanning — dropped at
-- the bottom of this Up block since the prefix-scan is one-shot and
-- audit_log already has its own usage indexes. Re-creates trivially
-- if needed for future backfills.
CREATE INDEX IF NOT EXISTS tmp_idx_audit_log_resource_name_prefix
    ON audit_log (resource_name text_pattern_ops);

UPDATE audit_log AS al
   SET resource_name = 'storageBackends/' || ok.backend_id
                     || '/buckets/'        || ok.bucket_name
                     || '/tenants/'        || ok.tenant_id::text
                     || '/objectKeys/'     || ok.object_key
  FROM object_keys AS ok
 WHERE al.resource_name LIKE 'tenants/%/objectKeys/%'
   AND al.resource_name = 'tenants/' || ok.tenant_id::text
                       || '/objectKeys/' || ok.object_key;

DROP INDEX IF EXISTS tmp_idx_audit_log_resource_name_prefix;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Reverse rewrite — strip the storageBackends/...{tenants/ prefix so
-- canonical names collapse back to the C-shape. Two-step because the
-- substring(...) result starts with `/tenants/` and we need to drop
-- the leading slash.
UPDATE audit_log
   SET resource_name = substring(resource_name from '/tenants/.*')
 WHERE resource_name LIKE 'storageBackends/%/buckets/%/tenants/%/objectKeys/%';
UPDATE audit_log
   SET resource_name = substring(resource_name from 2)
 WHERE resource_name LIKE '/tenants/%/objectKeys/%';
-- +goose StatementEnd
