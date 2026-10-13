-- +goose Up
-- +goose StatementBegin

-- An API token's rate limit of NULL is no cap; 0 admits no request. A cap of
-- 0 used to mean "none", so a reader could not tell an uncapped token from
-- one that admits nothing. Every 0 written under the old reading becomes
-- NULL. The >= 0 check stays: NULL passes it.
ALTER TABLE api_tokens
    ALTER COLUMN rate_limit_rpm DROP NOT NULL,
    ALTER COLUMN rate_limit_rpm DROP DEFAULT;
UPDATE api_tokens SET rate_limit_rpm = NULL WHERE rate_limit_rpm = 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- A cap of 0 has no spelling under the old reading; it goes back as none.
UPDATE api_tokens SET rate_limit_rpm = 0 WHERE rate_limit_rpm IS NULL;
ALTER TABLE api_tokens
    ALTER COLUMN rate_limit_rpm SET DEFAULT 0,
    ALTER COLUMN rate_limit_rpm SET NOT NULL;

-- +goose StatementEnd
