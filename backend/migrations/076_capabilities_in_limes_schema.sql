-- +goose Up
-- Capabilities, their meter and the tenant budgets move to the limes schema,
-- which github.com/oleg-tkachuk/limes/pgstore creates and migrates itself
-- (store/postgres.MigrateLimes, which runs before goose). Nothing is carried
-- over: every capability, revocation, counter, hold, charge and tenant budget
-- recorded so far is dropped, and capabilities are issued again. IF EXISTS
-- lets the migration run again after its Down, which recreates none of them.
--
-- dpop_seen_proofs stays: the DPoP replay cache is Paladin's own.

-- +goose StatementBegin
DROP TABLE IF EXISTS charge_refunds;
DROP TABLE IF EXISTS charges;
DROP TABLE IF EXISTS capability_reservations;
DROP TABLE IF EXISTS capability_copy_usage;
DROP TABLE IF EXISTS capability_usage;
DROP TABLE IF EXISTS capability_biscuit_revocations;
DROP TABLE IF EXISTS capability_revocations;
DROP TABLE IF EXISTS capability_records;
DROP TABLE IF EXISTS tenant_budgets;
-- +goose StatementEnd

-- The runtime roles reach pgstore's tables as they reach Paladin's: the
-- schema's row-level security, not grants, keeps tenants apart. The default
-- privileges cover a table a later pgstore migration adds.
-- +goose StatementBegin
DO $$
BEGIN
    GRANT USAGE ON SCHEMA limes TO paladin_app, paladin_reaper;
    GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA limes TO paladin_app, paladin_reaper;
    ALTER DEFAULT PRIVILEGES FOR ROLE current_user IN SCHEMA limes
        GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO paladin_app, paladin_reaper;

    -- A revocation on any replica clears every replica's revocation cache
    -- (capability/postgres.RevocationWatcher), as 033 and 038 did for the
    -- tables dropped above.
    CREATE TRIGGER revocations_notify
        AFTER INSERT ON limes.revocations
        FOR EACH STATEMENT EXECUTE FUNCTION paladin_notify_capability_revoked();
    CREATE TRIGGER biscuit_revocations_notify
        AFTER INSERT ON limes.biscuit_revocations
        FOR EACH STATEMENT EXECUTE FUNCTION paladin_notify_capability_revoked();
END
$$;
-- +goose StatementEnd

-- +goose Down
-- The dropped tables are not recreated: the code that read them is gone, and
-- their rows were dropped with them.
-- +goose StatementBegin
DO $$
BEGIN
    DROP TRIGGER IF EXISTS revocations_notify ON limes.revocations;
    DROP TRIGGER IF EXISTS biscuit_revocations_notify ON limes.biscuit_revocations;
END
$$;
-- +goose StatementEnd
