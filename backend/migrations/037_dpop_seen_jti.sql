-- +goose Up
-- +goose StatementBegin

-- DPoP proof ids (RFC 9449 jti) already accepted, shared by every replica so a
-- proof replayed against another one inside the acceptance window is refused
-- there too. A row lives until its proof could no longer be accepted anyway;
-- the capability purger deletes it after that.
--
-- No row level security: a proof id belongs to no tenant. It is checked
-- before the request is scoped to one, and its uniqueness must hold across
-- all of them — a policy would split the one namespace the check relies on.
-- The table holds nothing but random ids and their expiry.
--
-- A new, empty table, so its index is built in the same transaction.
CREATE TABLE dpop_seen_jti (
    jti        text PRIMARY KEY,
    expires_at timestamptz NOT NULL
);
CREATE INDEX dpop_seen_jti_expiry_idx ON dpop_seen_jti (expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS dpop_seen_jti;
-- +goose StatementEnd
