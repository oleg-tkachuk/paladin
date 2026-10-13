-- +goose Up
-- +goose StatementBegin

-- An operation's progress gets its own column. The runner used to write its
-- {processed, total} snapshots into metadata, which holds the executor's
-- arguments, so while an operation ran — and for good once the stale-operation
-- reclaimer failed it — the row no longer said what it was for. metadata is now
-- written once, at creation; progress is NULL until the first report.
ALTER TABLE operations ADD COLUMN progress jsonb;

-- A row whose metadata is a snapshot has already lost its arguments; the
-- snapshot moves to where it belongs and metadata is left empty rather than
-- passing a progress report off as arguments.
UPDATE operations
   SET progress = convert_from(metadata, 'UTF8')::jsonb,
       metadata = NULL
 WHERE metadata IS NOT NULL
   AND pg_input_is_valid(convert_from(metadata, 'UTF8'), 'jsonb')
   AND jsonb_typeof(convert_from(metadata, 'UTF8')::jsonb) = 'object'
   AND (SELECT array_agg(k ORDER BY k) FROM jsonb_object_keys(convert_from(metadata, 'UTF8')::jsonb) AS k)
       = ARRAY['processed', 'total'];

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- The old reading kept the latest snapshot in metadata; a row with no
-- arguments left gets its snapshot back there.
UPDATE operations
   SET metadata = convert_to(progress::text, 'UTF8')
 WHERE metadata IS NULL
   AND progress IS NOT NULL;
ALTER TABLE operations DROP COLUMN progress;

-- +goose StatementEnd
