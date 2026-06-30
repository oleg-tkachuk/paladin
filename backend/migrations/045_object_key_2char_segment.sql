-- +goose Up
-- +goose StatementBegin
-- 045_object_key_2char_segment.sql
--
-- Fix object_key_format rejecting exactly-2-char path segments.
--
-- The per-segment pattern from migration 030,
-- `[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?`, matches a segment of length 1 (the
-- optional group skipped) or >= 3 (first char + {1,61} middle + last char) —
-- but NOT exactly 2: the inner group needs >= 2 chars ({1,61} is >= 1, plus
-- the trailing [a-z0-9]). So realistic 2-char segments — region / quarter
-- codes like `eu`, `us`, `qa`, `q1` — were rejected at insert.
--
-- Relax the quantifier {1,61} -> {0,61} so the group can match just its
-- trailing char, admitting 2-char segments while keeping the 1..63 per-segment
-- length, kebab-case rules, and the 255-char total. The migration-030 regex is
-- a strict subset of this one, so every existing row still passes — no backfill.
ALTER TABLE object_keys
    DROP CONSTRAINT IF EXISTS object_key_format;

ALTER TABLE object_keys
    ADD CONSTRAINT object_key_format CHECK (
        char_length(object_key) <= 255
        AND object_key ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(/[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$'
    );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Revert to the migration-030 form (2-char segments rejected again). This Down
-- fails if any object_key with a 2-char segment exists — rename/remove those
-- first.
ALTER TABLE object_keys
    DROP CONSTRAINT IF EXISTS object_key_format;

ALTER TABLE object_keys
    ADD CONSTRAINT object_key_format CHECK (
        char_length(object_key) <= 255
        AND object_key ~ '^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?(/[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?)*$'
    );
-- +goose StatementEnd
