-- +goose Up
-- +goose StatementBegin

-- Bucket quotas move out of `quotas` into a table of their own, outside RLS.
--
-- A bucket-scoped row had tenant_id NULL, and `quotas` carries the tenant
-- isolation policy (002_roles_and_rls.sql), which admits a row only when
-- tenant_id equals the session's tenant. NULL equals nothing, so through the
-- runtime pool the row was invisible to every tenant: SetQuota for a bucket
-- failed WITH CHECK, GetQuota returned NotFound, and the upload check never
-- saw the cap it was meant to enforce. Only the worker's BYPASSRLS pool could
-- read the rows, which is why the reconciler and the census looked fine.
--
-- A bucket's cap is platform configuration, like the bucket itself: it is not
-- any one tenant's data. So the table is deliberately left without RLS, the
-- same as `buckets` and `storage_backends`; writes are gated by the role and
-- Cedar checks in the quota handler. The cost, recorded in ADR-0024: anyone
-- allowed to read quotas sees a shared bucket's aggregate usage.

CREATE TABLE bucket_quotas (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    bucket_id           uuid NOT NULL UNIQUE REFERENCES buckets(id) ON DELETE CASCADE,
    -- 0 means "no cap", as in `quotas`.
    max_total_bytes     bigint NOT NULL DEFAULT 0,
    max_object_count    bigint NOT NULL DEFAULT 0,
    max_bytes_per_day   bigint NOT NULL DEFAULT 0,
    max_objects_per_day bigint NOT NULL DEFAULT 0,
    usage_total_bytes   bigint NOT NULL DEFAULT 0,
    usage_object_count  bigint NOT NULL DEFAULT 0,
    usage_bytes_today   bigint NOT NULL DEFAULT 0,
    usage_objects_today bigint NOT NULL DEFAULT 0,
    last_reset_at       timestamptz NOT NULL DEFAULT now(),
    resource_version    bigint NOT NULL DEFAULT 1,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

-- The 003_triggers.sql loop covers only the tables that existed then.
CREATE TRIGGER bucket_quotas_bump_resource_version BEFORE UPDATE ON bucket_quotas
    FOR EACH ROW EXECUTE FUNCTION bump_resource_version();

-- Keep each row's id: a quota id an operator already holds still resolves.
INSERT INTO bucket_quotas (
    id, bucket_id,
    max_total_bytes, max_object_count, max_bytes_per_day, max_objects_per_day,
    usage_total_bytes, usage_object_count, usage_bytes_today, usage_objects_today,
    last_reset_at, resource_version, created_at, updated_at)
SELECT id, bucket_id,
       max_total_bytes, max_object_count, max_bytes_per_day, max_objects_per_day,
       usage_total_bytes, usage_object_count, usage_bytes_today, usage_objects_today,
       last_reset_at, resource_version, created_at, updated_at
  FROM quotas
 WHERE bucket_id IS NOT NULL;

DELETE FROM quotas WHERE bucket_id IS NOT NULL;

-- Dropping the column takes quotas_has_scope and both partial unique indexes
-- with it. What remains is one quota per tenant, which ON CONFLICT (tenant_id)
-- needs a plain unique index to name.
ALTER TABLE quotas DROP COLUMN bucket_id;
ALTER TABLE quotas ALTER COLUMN tenant_id SET NOT NULL;
CREATE UNIQUE INDEX quotas_tenant_key ON quotas (tenant_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS quotas_tenant_key;
ALTER TABLE quotas ALTER COLUMN tenant_id DROP NOT NULL;
ALTER TABLE quotas ADD COLUMN bucket_id uuid REFERENCES buckets(id) ON DELETE CASCADE;
ALTER TABLE quotas ADD CONSTRAINT quotas_has_scope
    CHECK (tenant_id IS NOT NULL OR bucket_id IS NOT NULL);
CREATE UNIQUE INDEX quotas_tenant_key ON quotas (tenant_id) WHERE bucket_id IS NULL;
CREATE UNIQUE INDEX quotas_bucket_key ON quotas (bucket_id) WHERE tenant_id IS NULL;

INSERT INTO quotas (
    id, bucket_id,
    max_total_bytes, max_object_count, max_bytes_per_day, max_objects_per_day,
    usage_total_bytes, usage_object_count, usage_bytes_today, usage_objects_today,
    last_reset_at, resource_version, created_at, updated_at)
SELECT id, bucket_id,
       max_total_bytes, max_object_count, max_bytes_per_day, max_objects_per_day,
       usage_total_bytes, usage_object_count, usage_bytes_today, usage_objects_today,
       last_reset_at, resource_version, created_at, updated_at
  FROM bucket_quotas;

DROP TABLE bucket_quotas;

-- +goose StatementEnd
