-- +goose Up
-- +goose StatementBegin

-- The bucket an object's current bytes are in (ADR-0051). Every path that
-- reads, copies or deletes an object's bytes resolves them here; the
-- collection's binding decides only where new uploads are signed. NULL on a
-- PENDING row whose bytes are not confirmed anywhere yet.
--
-- Nullable with no default: metadata only (CONVENTIONS.md). The foreign key
-- is NOT VALID: every existing row is NULL until 124 backfills it, and a
-- validating scan of objects would hold its lock for nothing. New rows are
-- checked. ON DELETE RESTRICT: a bucket that still holds an object's bytes
-- cannot be deleted under it, as a bucket a collection is bound to cannot.
ALTER TABLE objects ADD COLUMN placement_bucket_id uuid;
ALTER TABLE objects ADD CONSTRAINT objects_placement_bucket_fk
    FOREIGN KEY (placement_bucket_id) REFERENCES buckets(id) ON DELETE RESTRICT NOT VALID;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE objects DROP CONSTRAINT objects_placement_bucket_fk;
ALTER TABLE objects DROP COLUMN placement_bucket_id;
-- +goose StatementEnd
