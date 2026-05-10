-- +goose Up
-- +goose StatementBegin

-- event_deliveries is the durable outbox for webhook fan-out. Replaces
-- the in-process synchronous Dispatcher.Dispatch path that ran inside
-- admin handlers. Producer (admin handler when an event fires) does
-- the subs-lookup + filter match in-line and INSERTs one row per
-- matching subscription. The standalone dispatcher pod consumes the
-- outbox via FOR UPDATE SKIP LOCKED, posts to sinks, and updates
-- status / attempts / last_error / next_attempt_at.
--
-- Multiple dispatcher replicas can run concurrently — SKIP LOCKED
-- gives each row to exactly one replica per poll cycle.
--
-- TestSubscription RPC bypasses this table entirely — the admin pod
-- still has Dispatcher.DeliverOne for the synchronous "click-to-test"
-- flow.
CREATE TABLE event_deliveries (
    id               UUID         PRIMARY KEY,
    tenant_id        UUID         NOT NULL REFERENCES tenants(tenant_id) ON DELETE CASCADE,
    subscription_id  UUID         NOT NULL,                    -- not FK: subs can be soft-deleted while deliveries linger
    event_type       TEXT         NOT NULL,
    event_at         TIMESTAMPTZ  NOT NULL,                    -- when the producing action occurred
    event_payload    JSONB        NOT NULL,                    -- the full Event struct as serialized at producer
    status           TEXT         NOT NULL DEFAULT 'pending',  -- pending | delivered | failed
    attempts         INT          NOT NULL DEFAULT 0,
    last_error       TEXT         NOT NULL DEFAULT '',
    last_status_code INT          NOT NULL DEFAULT 0,          -- HTTP status of last attempt; 0 = no attempt
    last_attempt_at  TIMESTAMPTZ,
    next_attempt_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    delivered_at     TIMESTAMPTZ,
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- Hot-path index: dispatcher pod polls "what's ready to attempt now?".
-- Partial index keeps it tiny since most rows trend to delivered/failed.
CREATE INDEX event_deliveries_ready_idx
    ON event_deliveries (next_attempt_at)
    WHERE status = 'pending';

-- Per-subscription rollup queries (UI: "deliveries for this sub").
CREATE INDEX event_deliveries_sub_idx
    ON event_deliveries (subscription_id, created_at DESC);

-- Per-tenant rollup (audit / debugging).
CREATE INDEX event_deliveries_tenant_idx
    ON event_deliveries (tenant_id, created_at DESC);

-- RLS — same tenant isolation as every other tenant-scoped table.
-- The dispatcher pod runs as paladin_migrate (BYPASSRLS) since the outbox
-- loop legitimately spans tenants; the admin producer path (paladin_app)
-- still gets policy enforcement on INSERT.
ALTER TABLE event_deliveries ENABLE ROW LEVEL SECURITY;
CREATE POLICY event_deliveries_tenant_isolation ON event_deliveries
    USING (tenant_id::text = current_setting('paladin.tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('paladin.tenant_id', true));

-- paladin_app needs full DML — producer (admin) inserts; dispatcher
-- (separate pod, same role baseline) updates. Both are GRANTed; the
-- dispatcher pod additionally runs as a BYPASSRLS role at connect-time
-- so cross-tenant scans work.
GRANT SELECT, INSERT, UPDATE, DELETE ON event_deliveries TO paladin_app;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS event_deliveries;
-- +goose StatementEnd
