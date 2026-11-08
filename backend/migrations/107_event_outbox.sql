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

-- A producer records an event for the tenant the changed row belongs to,
-- which is not always the session's: a platform admin creating a bucket for a
-- tenant, or trashing one, acts from its own. The tenant is taken from the
-- changed row, never from the request, so inserts are not pinned to the
-- session tenant. Reading and deleting stay pinned; the dispatcher does both
-- across tenants with BYPASSRLS.
ALTER TABLE event_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE event_outbox FORCE ROW LEVEL SECURITY;
CREATE POLICY event_outbox_record ON event_outbox FOR INSERT
    WITH CHECK (true);
CREATE POLICY event_outbox_tenant_read ON event_outbox FOR SELECT
    USING (tenant_id = paladin_session_tenant_id()
           OR paladin_session_cross_tenant());
CREATE POLICY event_outbox_tenant_delete ON event_outbox FOR DELETE
    USING (tenant_id = paladin_session_tenant_id());

-- +goose Down
DROP TABLE event_outbox;
