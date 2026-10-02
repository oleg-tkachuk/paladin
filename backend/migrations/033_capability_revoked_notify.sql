-- +goose Up
-- +goose StatementBegin

-- Revocations reach other replicas by notification instead of by cache expiry.
--
-- Every verifier caches revocation answers for revocation_cache_ttl. The
-- replica that revoked invalidates its own entry; every other replica used to
-- serve "live" until its entry expired. This trigger announces each revoking
-- statement on channel `capability_revoked`
-- (internal/capability/postgres/revocation_watch.go), and each listener clears
-- its whole cache: an answer cached for a descendant is also an answer about
-- the revoked ancestor, so clearing one id would not be enough.
--
-- Statement-level, so a cascade that revokes a thousand descendants sends one
-- notification, not a thousand. The payload carries nothing a listener needs;
-- it is "*" so the channel's contract does not depend on which rows changed.
-- Notifications are delivered at commit, so a rolled-back revoke announces
-- nothing.
CREATE OR REPLACE FUNCTION paladin_notify_capability_revoked() RETURNS trigger
    LANGUAGE plpgsql SET search_path TO 'pg_catalog', 'public' AS $$
BEGIN
    PERFORM pg_notify('capability_revoked', '*');
    RETURN NULL;
END
$$;

CREATE TRIGGER capability_revocations_notify
    AFTER INSERT ON capability_revocations
    FOR EACH STATEMENT EXECUTE FUNCTION paladin_notify_capability_revoked();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS capability_revocations_notify ON capability_revocations;
DROP FUNCTION IF EXISTS paladin_notify_capability_revoked();
-- +goose StatementEnd
