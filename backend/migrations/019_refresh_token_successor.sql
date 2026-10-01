-- +goose Up
-- +goose StatementBegin

-- A rotation whose response is lost must not end the session.
--
-- Rotation supersedes the presented token and hands back a successor. If that
-- response never reaches the client — a browser navigating away while it is in
-- flight drops the Set-Cookie — the client still holds the superseded token.
-- Inside the 30-second supersession grace that is forgiven; after it, the next
-- presentation is indistinguishable from a replay, reuse detection revokes the
-- family, and a session nothing was wrong with is over.
--
-- What tells the two apart is whether the successor was ever used. A thief
-- replaying an old token arrives after the holder moved on, so the successor
-- has been presented. A client that lost the response never received the
-- successor, so it has not. These two columns record exactly that:
--
--   parent_id      the token this one was rotated from. NULL for a login, and
--                  for every row written before this migration — which is why
--                  no backfill is needed: a row without a parent is never
--                  offered as a lost successor.
--   first_used_at  when this token was first presented. NULL until then.
--
-- The handler re-issues an unused successor (same jti, so the store and reuse
-- detection see one token) to the holder of its parent for a bounded window.

ALTER TABLE refresh_tokens
    ADD COLUMN IF NOT EXISTS parent_id uuid,
    ADD COLUMN IF NOT EXISTS first_used_at timestamptz;

COMMENT ON COLUMN refresh_tokens.parent_id IS
    'The token this one was rotated from; NULL for a login. Lets a lost rotation be recovered.';
COMMENT ON COLUMN refresh_tokens.first_used_at IS
    'When this token was first presented. An unused successor is what a lost rotation response leaves behind.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE refresh_tokens
    DROP COLUMN IF EXISTS first_used_at,
    DROP COLUMN IF EXISTS parent_id;
-- +goose StatementEnd
