-- +goose Up

-- Whether a client registered itself (RFC 7591) rather than being seeded from
-- auth.oauth.seed_clients. The reaper removes only these, and the console
-- marks them, so the answer has to be stored: comparing against the current
-- seed list called a client that dropped out of the configuration
-- self-registered.
--
-- A constant default, so adding the column is a catalog change only
-- (CONVENTIONS.md).
ALTER TABLE oauth_clients ADD COLUMN self_registered boolean NOT NULL DEFAULT false;

-- Clients registered before this column: dynamic registration names a client
-- "paladin-" and a UUIDv7, which no seeded client is named.
UPDATE oauth_clients
SET self_registered = true
WHERE client_id ~ '^paladin-[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$';

-- +goose Down
ALTER TABLE oauth_clients DROP COLUMN IF EXISTS self_registered;
