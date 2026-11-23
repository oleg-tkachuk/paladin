-- +goose Up
-- +goose StatementBegin

-- The bucket a PENDING object's upload URL names. A storage migration can
-- rebind the collection between the presign and the PUT: the bytes then land
-- in the bucket the URL was signed for, not the one the collection is bound
-- to by the time the upload completes. CompleteObject and the reconciler look
-- for the bytes here first and move them to the collection's bucket; the
-- migration's cleanup waits while an upload is still anchored to its source
-- (ADR-0015).
--
-- Set when the PENDING row is inserted, with the bucket the URL is signed
-- for, and never changed: a regenerated URL may name the current binding,
-- which the lookup falls back to. It means nothing once the object is
-- promoted. NULL — a row from before this file, or one created by a copy —
-- means the collection's current binding, as every row did before.
--
-- Nullable with no default: metadata only (CONVENTIONS.md). The foreign key
-- is NOT VALID: every existing row is NULL, so there is nothing to validate,
-- and a validating scan of objects would hold its lock for nothing. New rows
-- are checked. ON DELETE SET NULL: a deleted bucket leaves the row to fall
-- back on the binding rather than blocking the delete.
ALTER TABLE objects ADD COLUMN upload_bucket_id uuid;
ALTER TABLE objects ADD CONSTRAINT objects_upload_bucket_fk
    FOREIGN KEY (upload_bucket_id) REFERENCES buckets(id) ON DELETE SET NULL NOT VALID;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE objects DROP CONSTRAINT objects_upload_bucket_fk;
ALTER TABLE objects DROP COLUMN upload_bucket_id;
-- +goose StatementEnd
