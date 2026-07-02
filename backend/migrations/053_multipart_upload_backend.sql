-- +goose Up
-- +goose StatementBegin
-- 053_multipart_upload_backend.sql
--
-- Anchors a multipart upload to the (backend_id, bucket_name) it was
-- INITIATED against. Complete / abort / presign-part and the abandoned-upload
-- reaper previously re-resolved the physical location from the object_key's
-- CURRENT binding, so an operator BindObjectKeyToBucket between initiate and
-- complete would route those calls to the NEW backend while the uploaded parts
-- live on the OLD one — a pre-existing bucket-rebind gap that multi-backend
-- routing widens to whole backends. Storing the location on the session makes
-- the entire upload lifecycle target where the parts actually are.
--
-- Existing in-flight rows get '' (NOT NULL DEFAULT) and fall back to
-- re-resolution — safe because they were initiated before any rebind support
-- for their session and drain within the reaper TTL.
ALTER TABLE multipart_uploads
    ADD COLUMN backend_id  TEXT NOT NULL DEFAULT '',
    ADD COLUMN bucket_name TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE multipart_uploads
    DROP COLUMN backend_id,
    DROP COLUMN bucket_name;
-- +goose StatementEnd
