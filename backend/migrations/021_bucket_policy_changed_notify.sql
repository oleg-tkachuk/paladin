-- +goose Up
-- +goose StatementBegin

-- A bucket's cedar_policy is a policy layer for every collection bound to the
-- bucket (internal/policy/cedar/store.go). Its change has to reach the engine
-- the way tenant and collection changes do, on channel `policy_changed`.
--
-- 003_triggers.sql left buckets out on the understanding that the admin plane
-- flushed the engine on a bucket policy write; nothing did. A bucket's
-- collections span tenants, so the payload cannot name a scope: "*" asks every
-- listener to drop its whole compiled cache (cedar.NotifyAllScopes).
--
-- As in 003, the guard compares the policy text, and INSERT and DELETE notify
-- only when the row carries a policy.
CREATE OR REPLACE FUNCTION paladin_notify_bucket_policy_changed() RETURNS trigger
    LANGUAGE plpgsql SET search_path TO 'pg_catalog', 'public' AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF OLD.cedar_policy <> '' THEN
            PERFORM pg_notify('policy_changed', '*');
        END IF;
        RETURN OLD;
    END IF;
    IF TG_OP = 'INSERT' THEN
        IF NEW.cedar_policy <> '' THEN
            PERFORM pg_notify('policy_changed', '*');
        END IF;
        RETURN NEW;
    END IF;
    IF NEW.cedar_policy IS DISTINCT FROM OLD.cedar_policy THEN
        PERFORM pg_notify('policy_changed', '*');
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER buckets_notify_policy_changed
    AFTER INSERT OR UPDATE OR DELETE ON buckets
    FOR EACH ROW EXECUTE FUNCTION paladin_notify_bucket_policy_changed();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS buckets_notify_policy_changed ON buckets;
DROP FUNCTION IF EXISTS paladin_notify_bucket_policy_changed();
-- +goose StatementEnd
