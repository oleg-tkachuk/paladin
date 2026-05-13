-- +goose Up
-- +goose StatementBegin
-- 030_object_key_multisegment.sql
--
-- The original `object_key_format` constraint required a single
-- kebab-case segment (1-63 chars, [a-z0-9-]). The new format allows
-- slash-separated paths like `invoices/2026/q1` so operators can
-- carve a tenant's namespace into a hierarchy without inventing a
-- separate bucket per folder.
--
-- Per-segment rules stay the same as before — kebab-case, 1-63
-- chars, starts/ends alphanumeric. Up to N segments joined by `/`,
-- total length ≤ 255 (well under PostgreSQL's TEXT limit and
-- generous enough for "year/quarter/month/team" depth).
--
-- The previous regex `^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?$` is a
-- strict subset of the new one, so every existing row passes the
-- new check unchanged. No backfill or value rewrite needed.
--
-- S3-side note: object_key participates in the storage path as
-- `<bucket>/<tenant_id>/<object_key>/<key>`. With slashes inside
-- object_key, the storage adapter and event-ingest path parsers
-- need longest-prefix-match to know where object_key ends and the
-- user-supplied `key` begins. That work is BACKLOG-tracked and
-- doesn't block the constraint relaxation.

ALTER TABLE object_keys
    DROP CONSTRAINT IF EXISTS object_key_format;

ALTER TABLE object_keys
    ADD CONSTRAINT object_key_format CHECK (
        char_length(object_key) <= 255
        AND object_key ~ '^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?(/[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?)*$'
    );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE object_keys
    DROP CONSTRAINT IF EXISTS object_key_format;

ALTER TABLE object_keys
    ADD CONSTRAINT object_key_format CHECK (
        object_key ~ '^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?$'
    );
-- +goose StatementEnd
