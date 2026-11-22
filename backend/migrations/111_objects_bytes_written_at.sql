-- +goose Up
-- +goose StatementBegin

-- When the bytes under an object's key were last written, apart from any
-- other change to its row. A storage migration's rollback copies back the
-- objects whose bytes were written since the rebind (ADR-0015); updated_at,
-- which it used to read, moves on every UPDATE — a tag, a taint, a lock, a
-- trash move — and so brought in objects whose only copy in the dedicated
-- bucket could be the broken one the verify failed on.
--
-- The promote sets it (statemachine.Transitioner.promote): the first one,
-- once an upload's bytes are confirmed, and every later one, a storage event
-- reporting a newer write under the key. Nothing else writes bytes under a
-- key: a restore from the trash or of a version moves a row or a pointer
-- only.
--
-- Nullable with no default: a metadata-only change (CONVENTIONS.md). A row
-- promoted before this file has none, and the rollback reads updated_at for
-- it, as it did for every row before — it may copy back more than it must,
-- never less.
ALTER TABLE objects ADD COLUMN bytes_written_at timestamptz;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE objects DROP COLUMN bytes_written_at;
-- +goose StatementEnd
