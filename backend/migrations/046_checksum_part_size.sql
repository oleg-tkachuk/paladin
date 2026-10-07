-- +goose Up
-- +goose StatementBegin

-- The part size of an object assembled from a multipart upload, beside its
-- composite checksum.
--
-- A multipart object's checksum is the digest of its parts' digests, so a
-- reader can only verify it knowing where each part ends: every part is this
-- many bytes, the last the remainder. The object's size and part count do not
-- fix it on their own. NULL for an object uploaded whole, whose checksum is a
-- digest of the bytes, and for a multipart object completed before this
-- column existed, which has no checksum recorded at all.
--
-- Nullable with no default: a metadata-only change on both tables.
ALTER TABLE objects ADD COLUMN checksum_part_size_bytes bigint;
ALTER TABLE object_versions ADD COLUMN checksum_part_size_bytes bigint;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE object_versions DROP COLUMN IF EXISTS checksum_part_size_bytes;
ALTER TABLE objects DROP COLUMN IF EXISTS checksum_part_size_bytes;

-- +goose StatementEnd
