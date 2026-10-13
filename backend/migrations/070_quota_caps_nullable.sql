-- +goose Up
-- +goose StatementBegin

-- A quota cap of NULL is no cap; 0 is a cap that allows nothing. A cap of 0
-- used to mean "none", so a quota could not stop a tenant or a bucket from
-- storing anything without being deleted, and a reader could not tell the two
-- apart. Every 0 written under the old reading becomes NULL.
ALTER TABLE quotas
    ALTER COLUMN max_total_bytes DROP NOT NULL,
    ALTER COLUMN max_total_bytes DROP DEFAULT,
    ALTER COLUMN max_object_count DROP NOT NULL,
    ALTER COLUMN max_object_count DROP DEFAULT,
    ALTER COLUMN max_bytes_per_day DROP NOT NULL,
    ALTER COLUMN max_bytes_per_day DROP DEFAULT,
    ALTER COLUMN max_objects_per_day DROP NOT NULL,
    ALTER COLUMN max_objects_per_day DROP DEFAULT;
ALTER TABLE bucket_quotas
    ALTER COLUMN max_total_bytes DROP NOT NULL,
    ALTER COLUMN max_total_bytes DROP DEFAULT,
    ALTER COLUMN max_object_count DROP NOT NULL,
    ALTER COLUMN max_object_count DROP DEFAULT,
    ALTER COLUMN max_bytes_per_day DROP NOT NULL,
    ALTER COLUMN max_bytes_per_day DROP DEFAULT,
    ALTER COLUMN max_objects_per_day DROP NOT NULL,
    ALTER COLUMN max_objects_per_day DROP DEFAULT;

UPDATE quotas SET
    max_total_bytes     = NULLIF(max_total_bytes, 0),
    max_object_count    = NULLIF(max_object_count, 0),
    max_bytes_per_day   = NULLIF(max_bytes_per_day, 0),
    max_objects_per_day = NULLIF(max_objects_per_day, 0);
UPDATE bucket_quotas SET
    max_total_bytes     = NULLIF(max_total_bytes, 0),
    max_object_count    = NULLIF(max_object_count, 0),
    max_bytes_per_day   = NULLIF(max_bytes_per_day, 0),
    max_objects_per_day = NULLIF(max_objects_per_day, 0);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- A cap of 0 has no spelling under the old reading; it goes back as none.
UPDATE quotas SET
    max_total_bytes     = COALESCE(max_total_bytes, 0),
    max_object_count    = COALESCE(max_object_count, 0),
    max_bytes_per_day   = COALESCE(max_bytes_per_day, 0),
    max_objects_per_day = COALESCE(max_objects_per_day, 0);
UPDATE bucket_quotas SET
    max_total_bytes     = COALESCE(max_total_bytes, 0),
    max_object_count    = COALESCE(max_object_count, 0),
    max_bytes_per_day   = COALESCE(max_bytes_per_day, 0),
    max_objects_per_day = COALESCE(max_objects_per_day, 0);
ALTER TABLE quotas
    ALTER COLUMN max_total_bytes SET DEFAULT 0,
    ALTER COLUMN max_total_bytes SET NOT NULL,
    ALTER COLUMN max_object_count SET DEFAULT 0,
    ALTER COLUMN max_object_count SET NOT NULL,
    ALTER COLUMN max_bytes_per_day SET DEFAULT 0,
    ALTER COLUMN max_bytes_per_day SET NOT NULL,
    ALTER COLUMN max_objects_per_day SET DEFAULT 0,
    ALTER COLUMN max_objects_per_day SET NOT NULL;
ALTER TABLE bucket_quotas
    ALTER COLUMN max_total_bytes SET DEFAULT 0,
    ALTER COLUMN max_total_bytes SET NOT NULL,
    ALTER COLUMN max_object_count SET DEFAULT 0,
    ALTER COLUMN max_object_count SET NOT NULL,
    ALTER COLUMN max_bytes_per_day SET DEFAULT 0,
    ALTER COLUMN max_bytes_per_day SET NOT NULL,
    ALTER COLUMN max_objects_per_day SET DEFAULT 0,
    ALTER COLUMN max_objects_per_day SET NOT NULL;

-- +goose StatementEnd
