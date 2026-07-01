-- +goose Up
-- +goose StatementBegin
-- 050_audit_log_notify.sql
--
-- Realtime audit feed: every audit_log insert fires a Postgres NOTIFY on
-- channel "paladin_audit" so the admin plane's SSE stream (/audit/stream) can
-- push new entries to the console without polling.
--
-- Payload is a COMPACT JSON projection of the row — deliberately excluding
-- before_json / after_json (they can be arbitrarily large and NOTIFY payloads
-- are capped at ~8000 bytes; a client that needs the diff fetches the entry
-- by id via GetAuditLogEntry). error_message is truncated for the same
-- reason. A trigger (vs emitting from Go) catches every producer — admin,
-- api, dispatcher, ingest — without each pod having to remember to notify.
--
-- audit_log is partitioned (041); a row-level trigger on the partitioned
-- parent cascades to all partitions, present and future.
CREATE OR REPLACE FUNCTION paladin_notify_audit() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('paladin_audit', json_build_object(
        'entry_id',        NEW.entry_id,
        'at',              to_char(NEW.at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
        'actor_subject',   NEW.actor_subject,
        'actor_tenant_id', NEW.actor_tenant_id,
        'actor_audience',  NEW.actor_audience,
        'action',          NEW.action,
        'resource_name',   left(NEW.resource_name, 512),
        'error_message',   left(NEW.error_message, 500)
    )::text);
    RETURN NULL; -- AFTER trigger: return value ignored
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER audit_log_notify
AFTER INSERT ON audit_log
FOR EACH ROW EXECUTE FUNCTION paladin_notify_audit();

-- +goose Down
DROP TRIGGER IF EXISTS audit_log_notify ON audit_log;
DROP FUNCTION IF EXISTS paladin_notify_audit();
