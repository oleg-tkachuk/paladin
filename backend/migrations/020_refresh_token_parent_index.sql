-- +goose NO TRANSACTION
-- +goose Up

-- The lost-rotation lookup is "the unused successor of this jti". Only rotated
-- rows carry a parent, so the index covers those alone. CONCURRENTLY, and on
-- its own, per CONVENTIONS.md: refresh_tokens is written on every login.
CREATE INDEX CONCURRENTLY IF NOT EXISTS refresh_tokens_parent_id_idx
    ON refresh_tokens (parent_id)
    WHERE parent_id IS NOT NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS refresh_tokens_parent_id_idx;
