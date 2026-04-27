-- +goose Up
-- +goose StatementBegin

-- Enforce NOT NULL on object_keys.bucket_name. By the time this migration
-- runs the application's startup hook (backfillObjectKeyBuckets) has had
-- a chance to populate any legacy NULL rows from
-- `storage.backends.<backend_id>.bucket`. Migrations run BEFORE startup
-- backfill though, so we apply a safety-net backfill here too.
--
-- The safety-net works by joining storage_backends → buckets where the
-- bucket name matches a backend's *one* registered bucket; that's the
-- common single-bucket-per-backend case. Operators with multiple buckets
-- per backend should run their own backfill before this migration ships.

UPDATE object_keys ok
SET bucket_name = b.bucket_name
FROM (
    SELECT backend_id, bucket_name
    FROM (
        SELECT backend_id, bucket_name,
               ROW_NUMBER() OVER (PARTITION BY backend_id ORDER BY created_at) AS rn,
               COUNT(*)    OVER (PARTITION BY backend_id) AS bucket_count
        FROM buckets
    ) ranked
    WHERE rn = 1 AND bucket_count = 1
) b
WHERE ok.bucket_name IS NULL
  AND ok.backend_id = b.backend_id;

-- Refuse to apply NOT NULL if any row is still NULL — surface the
-- inconsistency loudly rather than letting startup backfill fix it later
-- (which would require a different migration sequence).
DO $$
DECLARE
    null_count bigint;
BEGIN
    SELECT count(*) INTO null_count FROM object_keys WHERE bucket_name IS NULL;
    IF null_count > 0 THEN
        RAISE EXCEPTION 'Cannot enforce NOT NULL on object_keys.bucket_name: % rows still NULL. Backfill required (e.g. SET bucket_name = ''<bucket>'' WHERE backend_id = ''<backend>'').', null_count;
    END IF;
END $$;

ALTER TABLE object_keys ALTER COLUMN bucket_name SET NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE object_keys ALTER COLUMN bucket_name DROP NOT NULL;
-- +goose StatementEnd
