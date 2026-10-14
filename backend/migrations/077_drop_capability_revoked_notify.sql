-- +goose Up
-- pgstore's own schema notifies limes_revocations on every revocation
-- (pgstore.RevocationChannel), and pgstore.RevocationWatcher clears each
-- replica's caches on it, so the triggers 076 hung on pgstore's tables and
-- the function behind them, which announced on capability_revoked, go.
-- The DO block keeps the limes schema out of sqlc's view, as in 076.

-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS revocations_notify ON limes.revocations;
    DROP TRIGGER IF EXISTS biscuit_revocations_notify ON limes.biscuit_revocations;
END
$$;
DROP FUNCTION IF EXISTS paladin_notify_capability_revoked();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION paladin_notify_capability_revoked() RETURNS trigger
    LANGUAGE plpgsql SET search_path TO 'pg_catalog', 'public' AS $$
BEGIN
    PERFORM pg_notify('capability_revoked', '*');
    RETURN NULL;
END
$$;
DO $$
BEGIN
    CREATE TRIGGER revocations_notify
        AFTER INSERT ON limes.revocations
        FOR EACH STATEMENT EXECUTE FUNCTION paladin_notify_capability_revoked();
    CREATE TRIGGER biscuit_revocations_notify
        AFTER INSERT ON limes.biscuit_revocations
        FOR EACH STATEMENT EXECUTE FUNCTION paladin_notify_capability_revoked();
END
$$;
-- +goose StatementEnd
