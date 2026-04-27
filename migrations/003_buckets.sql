-- +goose Up
-- +goose StatementBegin

-- ─── Recovery: legacy "buckets" → "object_keys" rename ────────────────────
-- Goose tracks version numbers, not file content. Databases that ran
-- migration 001 *before* the PALADIN "Bucket → ObjectKey" rename still have a
-- `buckets` table (with column `bucket_id`) and no `object_keys` table.
-- Migration 001 was rewritten in-place but goose won't re-run it. So we
-- pick up the slack here: if the legacy shape is detected, perform the
-- rename so the rest of this migration (and downstream code) sees the
-- canonical `object_keys` table.
DO $$
DECLARE
    has_legacy_buckets boolean;
    has_object_keys    boolean;
    has_bucket_id_col  boolean;
BEGIN
    SELECT EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_schema = 'public' AND table_name = 'buckets'
    ) INTO has_legacy_buckets;

    SELECT EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_schema = 'public' AND table_name = 'object_keys'
    ) INTO has_object_keys;

    IF has_legacy_buckets THEN
        SELECT EXISTS (
            SELECT 1 FROM information_schema.columns
            WHERE table_schema = 'public'
              AND table_name = 'buckets'
              AND column_name = 'bucket_id'
        ) INTO has_bucket_id_col;
    ELSE
        has_bucket_id_col := false;
    END IF;

    IF has_legacy_buckets AND NOT has_object_keys AND has_bucket_id_col THEN
        -- 1. Rename legacy `objects.bucket_id` column to `object_key`.
        IF EXISTS (
            SELECT 1 FROM information_schema.columns
            WHERE table_schema = 'public' AND table_name = 'objects' AND column_name = 'bucket_id'
        ) THEN
            -- Drop the legacy FK first so the column rename + parent rename
            -- can happen without ordering issues.
            EXECUTE 'ALTER TABLE objects DROP CONSTRAINT IF EXISTS objects_tenant_id_bucket_id_fkey';
            EXECUTE 'ALTER TABLE objects RENAME COLUMN bucket_id TO object_key';
        END IF;

        -- 2. Rename the `buckets` table → `object_keys` and its key column.
        EXECUTE 'ALTER TABLE buckets RENAME TO object_keys';
        EXECUTE 'ALTER TABLE object_keys RENAME COLUMN bucket_id TO object_key';

        -- 3. Drop legacy CHECK constraint (its definition referenced bucket_id)
        --    and add the renamed one.
        EXECUTE 'ALTER TABLE object_keys DROP CONSTRAINT IF EXISTS bucket_id_format';
        EXECUTE $sql$ALTER TABLE object_keys ADD CONSTRAINT object_key_format
                     CHECK (object_key ~ '^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?$')$sql$;

        -- 4. Rename indexes that reference the old name.
        IF EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = 'public' AND indexname = 'idx_buckets_storage_backend') THEN
            EXECUTE 'ALTER INDEX idx_buckets_storage_backend RENAME TO idx_object_keys_storage_backend';
        END IF;

        -- 5. Recreate the bump-rv trigger under the new table name.
        EXECUTE 'DROP TRIGGER IF EXISTS trg_buckets_bump_rv ON object_keys';
        EXECUTE 'CREATE TRIGGER trg_object_keys_bump_rv
                 BEFORE UPDATE ON object_keys
                 FOR EACH ROW EXECUTE FUNCTION bump_resource_version()';

        -- 6. Re-establish the FK from objects → object_keys.
        EXECUTE 'ALTER TABLE objects ADD CONSTRAINT objects_tenant_id_object_key_fkey
                 FOREIGN KEY (tenant_id, object_key)
                 REFERENCES object_keys(tenant_id, object_key)
                 ON DELETE RESTRICT';
    END IF;
END $$;

-- ─── Canonical schema ─────────────────────────────────────────────────────
-- buckets tracks physical S3 buckets that have been provisioned inside a
-- storage backend (storage.backends.<id> in config). Buckets are explicitly
-- created via BucketService.CreateBucket; the adapter calls the underlying
-- S3 API and the row records the provisioned bucket so ObjectKey can FK to it.
--
-- Hierarchy: storage_backend → bucket → object_key → object
--   s3://<bucket>/<tenant_id>/<object_key>/<storage_key>

-- After the recovery block above the legacy `buckets` table is gone (was
-- renamed to object_keys). Any non-canonical `buckets` left from a
-- half-applied earlier attempt also needs to go.
DROP TABLE IF EXISTS buckets CASCADE;

CREATE TABLE buckets (
    backend_id       TEXT NOT NULL REFERENCES storage_backends(id) ON DELETE RESTRICT,
    bucket_name      TEXT NOT NULL,
    display_name     TEXT,
    region           TEXT,
    -- Free-form labels for filtering / cost-allocation tags.
    labels           JSONB NOT NULL DEFAULT '{}'::jsonb,
    resource_version BIGINT NOT NULL DEFAULT 1,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (backend_id, bucket_name),
    -- S3 bucket name rules: lowercase, 3-63 chars, alphanumeric + hyphen.
    CONSTRAINT bucket_name_format CHECK (bucket_name ~ '^[a-z0-9]([a-z0-9.-]{1,61}[a-z0-9])?$')
);

CREATE INDEX idx_buckets_labels_gin ON buckets USING GIN (labels);

-- object_keys gains a bucket_name column that points at a row in `buckets`.
-- Idempotent against partial-state DBs.
ALTER TABLE object_keys
    ADD COLUMN IF NOT EXISTS bucket_name TEXT;

ALTER TABLE object_keys
    DROP CONSTRAINT IF EXISTS object_keys_bucket_fk;
ALTER TABLE object_keys
    ADD CONSTRAINT object_keys_bucket_fk
        FOREIGN KEY (storage_backend, bucket_name)
        REFERENCES buckets(backend_id, bucket_name)
        ON DELETE RESTRICT
        DEFERRABLE INITIALLY DEFERRED;

CREATE INDEX IF NOT EXISTS idx_object_keys_bucket
    ON object_keys(storage_backend, bucket_name);

-- Bump-rv trigger for buckets, mirroring the pattern for tenants/object_keys.
DROP TRIGGER IF EXISTS trg_buckets_bump_rv ON buckets;
CREATE TRIGGER trg_buckets_bump_rv
    BEFORE UPDATE ON buckets
    FOR EACH ROW EXECUTE FUNCTION bump_resource_version();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_buckets_bump_rv ON buckets;
ALTER TABLE object_keys DROP CONSTRAINT IF EXISTS object_keys_bucket_fk;
DROP INDEX IF EXISTS idx_object_keys_bucket;
ALTER TABLE object_keys DROP COLUMN IF EXISTS bucket_name;
DROP TABLE IF EXISTS buckets;
-- +goose StatementEnd
