-- +goose Up
-- +goose StatementBegin

-- Validates what 132 added NOT VALID, on its own transaction: VALIDATE takes
-- SHARE UPDATE EXCLUSIVE, so reads and writes of objects go on during the
-- scan (CONVENTIONS.md). 132 backfilled every row these refuse.
ALTER TABLE objects VALIDATE CONSTRAINT objects_placed_past_pending;
ALTER TABLE objects VALIDATE CONSTRAINT objects_pending_anchored;
ALTER TABLE objects VALIDATE CONSTRAINT objects_upload_bucket_fk;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- A validated constraint cannot be marked NOT VALID again; 132's Down drops
-- them.
SELECT 1;
-- +goose StatementEnd
