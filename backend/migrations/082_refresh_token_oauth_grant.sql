-- +goose Up
-- +goose StatementBegin

-- A refresh token the OAuth Authorization Server issued records the grant it
-- came from: the client it was issued to, the audience it was issued for and
-- the scopes the user consented to (ADR-0035).
--
-- Without them the token endpoint could not tell its own tokens from the
-- console's, so either flow redeemed the other's, any client redeemed another
-- client's, and a rotation re-issued every token for paladin-data with no
-- scope. The three columns are set together or not at all: NULL on all three
-- is a console login, and the CHECK keeps a half-recorded grant out.
ALTER TABLE refresh_tokens
    ADD COLUMN IF NOT EXISTS oauth_client_id text
        REFERENCES oauth_clients (client_id) ON DELETE CASCADE,
    ADD COLUMN IF NOT EXISTS oauth_audience text,
    ADD COLUMN IF NOT EXISTS oauth_scopes text[],
    ADD CONSTRAINT refresh_tokens_oauth_grant_whole CHECK (
        (oauth_client_id IS NULL) = (oauth_audience IS NULL)
        AND (oauth_client_id IS NULL) = (oauth_scopes IS NULL)
    );

COMMENT ON COLUMN refresh_tokens.oauth_client_id IS
    'The OAuth client this token was issued to; NULL for a console login. Deleting the client deletes its tokens.';
COMMENT ON COLUMN refresh_tokens.oauth_audience IS
    'The audience the OAuth grant was issued for; a rotation re-issues the same one.';
COMMENT ON COLUMN refresh_tokens.oauth_scopes IS
    'The scopes the user consented to; a rotation re-issues the same set.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE refresh_tokens
    DROP CONSTRAINT IF EXISTS refresh_tokens_oauth_grant_whole,
    DROP COLUMN IF EXISTS oauth_scopes,
    DROP COLUMN IF EXISTS oauth_audience,
    DROP COLUMN IF EXISTS oauth_client_id;
-- +goose StatementEnd
