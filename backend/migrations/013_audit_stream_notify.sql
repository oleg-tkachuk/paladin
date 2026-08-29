-- +goose Up
-- +goose StatementBegin

-- The live audit stream has never delivered an event.
--
-- internal/auditstream/hub.go opens `LISTEN paladin_audit` and fans the payload
-- out to per-tenant SSE subscribers, and its own comments say the notification
-- comes from "the trigger baseline (003_triggers.sql)'s trigger" and from
-- "050's trigger". Neither exists: the only pg_notify in the migration set
-- fires on `policy_changed`, there is no migration 050, and `paladin_audit`
-- appears in no SQL at all. So the hub listened to a channel nothing wrote to.
--
-- The failure mode is the reason this went unnoticed for so long. The stream
-- connects, answers `: connected`, and sends `: ping` heartbeats forever, so
-- EventSource reports `open` and the console's live badge reads "connected"
-- while no audit entry ever arrives. A feature that is merely quiet looks
-- exactly like a system where nothing is happening.
--
-- The payload matches auditstream.Entry field for field. before_json /
-- after_json are deliberately excluded: NOTIFY payloads are capped at 8000
-- bytes and a diff can exceed that on its own, which would make the notify
-- fail and take the INSERT down with it. A client that wants the diff fetches
-- the entry by id.
CREATE OR REPLACE FUNCTION notify_audit_log() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    payload text;
BEGIN
    -- actor_tenant_id is the hub's routing key (Hub.Dispatch): a subscriber is
    -- registered under the viewer's own tenant, so an entry without one can
    -- reach nobody and is not worth a round trip.
    IF NEW.actor_tenant_id IS NULL THEN
        RETURN NULL;
    END IF;

    payload := json_build_object(
        'entry_id',        NEW.id::text,
        'at',              to_char(NEW.at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.USZ'),
        'actor_subject',   coalesce(NEW.actor_subject, ''),
        'actor_tenant_id', NEW.actor_tenant_id::text,
        'actor_audience',  coalesce(NEW.actor_audience, ''),
        'action',          coalesce(NEW.action, ''),
        'resource_name',   coalesce(NEW.resource_name, ''),
        'error_message',   coalesce(NEW.error_message, '')
    )::text;

    -- Guard the cap rather than let the INSERT fail. An audit row that cannot
    -- be announced must still be WRITTEN: the durable record is the point, the
    -- live view is a convenience. Truncating the two free-text fields is
    -- enough to bring any realistic payload under the limit.
    IF octet_length(payload) > 7500 THEN
        payload := json_build_object(
            'entry_id',        NEW.id::text,
            'at',              to_char(NEW.at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.USZ'),
            'actor_subject',   left(coalesce(NEW.actor_subject, ''), 256),
            'actor_tenant_id', NEW.actor_tenant_id::text,
            'actor_audience',  coalesce(NEW.actor_audience, ''),
            'action',          coalesce(NEW.action, ''),
            'resource_name',   left(coalesce(NEW.resource_name, ''), 1024),
            'error_message',   left(coalesce(NEW.error_message, ''), 1024)
        )::text;
    END IF;

    PERFORM pg_notify('paladin_audit', payload);
    RETURN NULL;
END;
$$;

-- audit_log is partitioned; a row-level AFTER trigger on the partitioned
-- parent propagates to every partition, existing and future (PG 13+). Putting
-- it on the parent is what keeps a newly created partition from silently
-- dropping out of the stream.
CREATE TRIGGER audit_log_notify
    AFTER INSERT ON audit_log
    FOR EACH ROW
    EXECUTE FUNCTION notify_audit_log();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS audit_log_notify ON audit_log;
DROP FUNCTION IF EXISTS notify_audit_log();
-- +goose StatementEnd
