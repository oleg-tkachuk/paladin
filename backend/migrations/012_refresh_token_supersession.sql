-- +goose Up
-- +goose StatementBegin

-- `revoked` is one boolean carrying two different facts, and the difference
-- matters at exactly one point.
--
-- A token is revoked when it is ROTATED (the normal path: the holder traded it
-- for a successor a moment ago) and also when it is REVOKED FOR CAUSE (logout,
-- or reuse detection revoking the whole family). To the column these look
-- identical, so a handler that wants to tolerate the first cannot, because it
-- would be tolerating the second at the same time.
--
-- That is what forced the console's BFF to keep the rotation chain in process
-- memory: the server could not say "this token was superseded three seconds
-- ago by its own holder", so the client had to remember it instead. That state
-- is why the console cannot run more than one replica.
--
-- superseded_at records the first fact only. It is written by rotation and by
-- nothing else: neither logout, nor RevokeForUser, nor RevokeFamilyOf touches
-- it, so a token revoked for cause is never mistaken for one that was merely
-- traded in.
ALTER TABLE refresh_tokens
    ADD COLUMN IF NOT EXISTS superseded_at timestamptz;

COMMENT ON COLUMN refresh_tokens.superseded_at IS
    'Set only when this token was rotated for a successor. NULL for tokens revoked for cause (logout, reuse detection), which must never be tolerated.';

-- The lookup is "was this one jti superseded, and when" — the primary key
-- already serves it. No index.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE refresh_tokens DROP COLUMN IF EXISTS superseded_at;
-- +goose StatementEnd
