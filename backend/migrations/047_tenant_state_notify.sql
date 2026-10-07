-- +goose Up
-- +goose StatementBegin

-- Every replica caches whether a tenant is live, so authentication can refuse
-- the credentials of a tenant in the trash without a query per request. This
-- trigger announces each statement that trashes, restores or removes a tenant
-- on channel `tenant_state` (internal/store/postgres/tenantstate), and each
-- listener clears its whole cache.
--
-- Statement-level, so a purge of many tenants sends one notification. The
-- payload is "*": the channel's contract does not depend on which rows
-- changed. Notifications are delivered at commit, so a rolled-back change
-- announces nothing. A DELETE is announced too: a purged tenant's JWTs still
-- verify on their signature alone.
CREATE OR REPLACE FUNCTION paladin_notify_tenant_state() RETURNS trigger
    LANGUAGE plpgsql SET search_path TO 'pg_catalog', 'public' AS $$
BEGIN
    PERFORM pg_notify('tenant_state', '*');
    RETURN NULL;
END
$$;

CREATE TRIGGER tenants_state_notify
    AFTER UPDATE OF deleted_at OR DELETE ON tenants
    FOR EACH STATEMENT EXECUTE FUNCTION paladin_notify_tenant_state();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS tenants_state_notify ON tenants;
DROP FUNCTION IF EXISTS paladin_notify_tenant_state();
-- +goose StatementEnd
