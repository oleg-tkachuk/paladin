-- +goose Up

-- An event is recorded once, on the producing transaction, and fanned out to
-- its subscriptions by the dispatcher (ADR-0044). A row lives here from the
-- producer's commit until the dispatcher has written its event_deliveries
-- rows, in the same transaction that deletes it.
CREATE TABLE event_outbox (
    id            uuid        PRIMARY KEY,
    tenant_id     uuid        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    event_type    text        NOT NULL,
    event_at      timestamptz NOT NULL,
    event_payload jsonb       NOT NULL,
    -- The database's time at the producing transaction, compared with each
    -- subscription's created_at at fan-out.
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- The fan-out claims the oldest rows first.
CREATE INDEX event_outbox_created_idx ON event_outbox (created_at);

-- Same isolation as event_deliveries: the producer path is checked on
-- insert; the dispatcher reads and deletes across tenants with BYPASSRLS.
ALTER TABLE event_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE event_outbox FORCE ROW LEVEL SECURITY;
CREATE POLICY event_outbox_tenant_isolation ON event_outbox FOR ALL
    USING (tenant_id = paladin_session_tenant_id())
    WITH CHECK (tenant_id = paladin_session_tenant_id());

-- +goose Down
DROP TABLE event_outbox;
