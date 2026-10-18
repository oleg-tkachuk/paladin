-- +goose Up

-- When the operation's initiator cleared it from their own list, the
-- console's operations drawer. A view of one person's, not a change to the
-- operation: the row stays, is listed to everyone else and to an explicit
-- include_dismissed, and is purged on the usual retention.
ALTER TABLE operations ADD COLUMN dismissed_at timestamptz;

-- +goose Down
ALTER TABLE operations DROP COLUMN IF EXISTS dismissed_at;
