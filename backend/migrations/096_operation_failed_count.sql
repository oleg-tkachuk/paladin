-- +goose Up

-- How many objects a batch could not act on, written with its result. A
-- batch that runs to its end SUCCEEDS whatever it could not do, so this is
-- what tells one that left objects behind from a clean one, and what a
-- filter finds them by. A constant default: a change of the catalog alone.
ALTER TABLE operations ADD COLUMN failed_count bigint NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE operations DROP COLUMN IF EXISTS failed_count;
