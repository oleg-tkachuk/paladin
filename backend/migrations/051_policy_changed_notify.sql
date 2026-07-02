-- +goose Up
-- +goose StatementBegin
-- 051_policy_changed_notify.sql
--
-- Cedar policy-cache invalidation: cedar.PostgresStore.Watch LISTENs on
-- channel "policy_changed", but until now nothing ever NOTIFYed it — the
-- engine's compiled-policy cache expired only via its ~30s TTL. These
-- triggers close the loop so a policy write invalidates within one event
-- round-trip instead of a TTL window.
--
-- Payload format (must match parseNotifyPayload in
-- internal/policy/cedar/store.go):
--   "<tenant_uuid>"               tenant-level change → engine drops every
--                                 cache entry for the tenant (the inherited
--                                 text is concatenated into all of them)
--   "<tenant_uuid>:<object_key>"  objectKey-scoped change → engine drops
--                                 that one entry
--
-- A trigger (vs emitting pg_notify from Go) catches every writer — admin
-- plane, seed jobs, manual psql — without each path having to remember to
-- notify (same rationale as 050_audit_log_notify). NOTIFY inside a
-- transaction is delivered only on commit, so listeners never see a policy
-- change that later rolls back. Payloads are a uuid plus an object_key,
-- far under the ~8000-byte NOTIFY cap.
--
-- INSERT/DELETE fire only when the policy text is non-empty: the engine
-- caches "no policy" results for unknown scopes (Fetch returns '' for a
-- missing row), so creating or dropping a row that carries a policy must
-- invalidate, while policy-less churn stays silent. UPDATE fires only on an
-- actual policy-text change (IS DISTINCT FROM), not on every row touch.
CREATE OR REPLACE FUNCTION paladin_notify_policy_changed_tenant() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM pg_notify('policy_changed', OLD.tenant_id::text);
    ELSE
        PERFORM pg_notify('policy_changed', NEW.tenant_id::text);
    END IF;
    RETURN NULL; -- AFTER trigger: return value ignored
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION paladin_notify_policy_changed_object_key() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM pg_notify('policy_changed', OLD.tenant_id::text || ':' || OLD.object_key);
    ELSE
        PERFORM pg_notify('policy_changed', NEW.tenant_id::text || ':' || NEW.object_key);
    END IF;
    RETURN NULL; -- AFTER trigger: return value ignored
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER tenants_policy_notify_ins
AFTER INSERT ON tenants
FOR EACH ROW WHEN (NEW.inherited_cedar_policy <> '')
EXECUTE FUNCTION paladin_notify_policy_changed_tenant();

CREATE TRIGGER tenants_policy_notify_upd
AFTER UPDATE ON tenants
FOR EACH ROW WHEN (OLD.inherited_cedar_policy IS DISTINCT FROM NEW.inherited_cedar_policy)
EXECUTE FUNCTION paladin_notify_policy_changed_tenant();

CREATE TRIGGER tenants_policy_notify_del
AFTER DELETE ON tenants
FOR EACH ROW WHEN (OLD.inherited_cedar_policy <> '')
EXECUTE FUNCTION paladin_notify_policy_changed_tenant();

CREATE TRIGGER object_keys_policy_notify_ins
AFTER INSERT ON object_keys
FOR EACH ROW WHEN (NEW.cedar_policy <> '')
EXECUTE FUNCTION paladin_notify_policy_changed_object_key();

CREATE TRIGGER object_keys_policy_notify_upd
AFTER UPDATE ON object_keys
FOR EACH ROW WHEN (OLD.cedar_policy IS DISTINCT FROM NEW.cedar_policy)
EXECUTE FUNCTION paladin_notify_policy_changed_object_key();

CREATE TRIGGER object_keys_policy_notify_del
AFTER DELETE ON object_keys
FOR EACH ROW WHEN (OLD.cedar_policy <> '')
EXECUTE FUNCTION paladin_notify_policy_changed_object_key();

-- +goose Down
DROP TRIGGER IF EXISTS tenants_policy_notify_ins ON tenants;
DROP TRIGGER IF EXISTS tenants_policy_notify_upd ON tenants;
DROP TRIGGER IF EXISTS tenants_policy_notify_del ON tenants;
DROP TRIGGER IF EXISTS object_keys_policy_notify_ins ON object_keys;
DROP TRIGGER IF EXISTS object_keys_policy_notify_upd ON object_keys;
DROP TRIGGER IF EXISTS object_keys_policy_notify_del ON object_keys;
DROP FUNCTION IF EXISTS paladin_notify_policy_changed_tenant();
DROP FUNCTION IF EXISTS paladin_notify_policy_changed_object_key();
