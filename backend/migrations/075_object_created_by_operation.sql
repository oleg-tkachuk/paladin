-- +goose Up
-- +goose StatementBegin

-- The operation that created an object row, when one did — a batch copy. A
-- copy whose worker died between creating its destination row and promoting
-- it leaves that row PENDING, and a reissue of the batch could not tell it
-- from a live upload to the same key, so it waited out the row's presign
-- deadline. With the creator known, a row whose creator is no longer running
-- can be adopted and finished; the column then names the adopter, which is
-- the operation now writing the row. No FK: operations are purged, and a row
-- whose creator is gone is as dead as one whose creator failed.
ALTER TABLE objects ADD COLUMN created_by_operation uuid;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE objects DROP COLUMN created_by_operation;

-- +goose StatementEnd
