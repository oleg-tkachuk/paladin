-- +goose Up
-- +goose StatementBegin

-- Two fixes that only surfaced once anything actually wrote object_locks.
--
-- 1. A legal hold could be placed and never lifted.
--
--    object_locks_asserts_something required mode OR legal_hold, so clearing
--    the hold on a row with no retention violated the CHECK. Deleting the row
--    instead hit object_locks_enforce_retention, which refuses to drop a row
--    under hold. Between them, a hold with no retention beside it was
--    permanent — the one lock that is supposed to be reversible.
--
--    A row asserting nothing is not a bug, it is a released lock. Keeping it
--    also keeps created_at/updated_at, which is the audit trail for when the
--    hold was placed and lifted.
--
-- 2. Retention could be weakened by anything that did not go through the
--    repository.
--
--    The rules lived only in SetObjectRetention's WHERE clause. That makes
--    them a property of one query rather than of the data, and COMPLIANCE is
--    supposed to be a property of the data: the point of the mode is that no
--    role can shorten it, and "no role" has to include one holding a psql
--    session. A plain UPDATE bypassed every rule.
--
--    The trigger below is the actual boundary; the WHERE clause stays because
--    it turns a refusal into zero rows, which the adapter reports as a clean
--    FailedPrecondition instead of a raised exception.

ALTER TABLE object_locks DROP CONSTRAINT IF EXISTS object_locks_asserts_something;

CREATE OR REPLACE FUNCTION enforce_object_lock_no_weakening() RETURNS trigger
    LANGUAGE plpgsql SET search_path TO 'pg_catalog', 'public' AS $$
DECLARE
    bypass  text := current_setting('paladin.bypass_governance_retention', true);
    active  boolean := OLD.retain_until IS NOT NULL AND OLD.retain_until > now();
    shorter boolean := NEW.retain_until IS NULL OR NEW.retain_until < OLD.retain_until;
BEGIN
    -- An expired window holds nothing, so anything may replace it. Without
    -- this an expired COMPLIANCE lock would be permanently un-relockable.
    IF NOT active THEN
        RETURN NEW;
    END IF;

    IF OLD.mode = 'COMPLIANCE' THEN
        IF shorter THEN
            RAISE EXCEPTION
                'object version % is retained under COMPLIANCE until %; the window cannot be shortened',
                OLD.version_id, OLD.retain_until
                USING ERRCODE = 'check_violation';
        END IF;
        IF NEW.mode IS DISTINCT FROM 'COMPLIANCE' THEN
            RAISE EXCEPTION
                'object version % is under COMPLIANCE retention; the mode cannot be downgraded',
                OLD.version_id
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    IF OLD.mode = 'GOVERNANCE' AND shorter AND bypass IS DISTINCT FROM 'on' THEN
        RAISE EXCEPTION
            'object version % is retained under GOVERNANCE until %; shortening requires the governance bypass',
            OLD.version_id, OLD.retain_until
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS object_locks_enforce_no_weakening ON object_locks;
CREATE TRIGGER object_locks_enforce_no_weakening
    BEFORE UPDATE ON object_locks
    FOR EACH ROW EXECUTE FUNCTION enforce_object_lock_no_weakening();

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS object_locks_enforce_no_weakening ON object_locks;
DROP FUNCTION IF EXISTS enforce_object_lock_no_weakening();
-- +goose StatementEnd
