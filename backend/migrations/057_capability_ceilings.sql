-- +goose Up

-- An ancestor's ceilings, in columns the charge path reads. They were read
-- out of the caveats JSON by key, and the key is limes's spelling: when limes
-- renamed it, every ancestor read as unlimited and nothing failed. 058 fills
-- them in for the rows already there.
ALTER TABLE capability_records ADD COLUMN max_budget numeric;
ALTER TABLE capability_records ADD COLUMN max_requests bigint;

-- +goose Down
ALTER TABLE capability_records DROP COLUMN max_requests;
ALTER TABLE capability_records DROP COLUMN max_budget;
