-- +goose Up
-- +goose StatementBegin

-- 069: purge debt for permanent object deletes.
--
-- The permanent-delete path drops the DB row first and the S3 bytes second.
-- That ordering is deliberate and stays (see the block comment in
-- object.Handler.DeleteObject): the reverse could delete a live object's bytes
-- and then fail the row delete, leaving a row pointing at nothing. The cost of
-- the safe ordering is the opposite residue — bytes with no row.
--
-- Until now that residue was unrecoverable. The row was gone, so there was
-- nothing to retry from; the client's retry got NotFound; and the "log loudly
-- so a sweeper can reclaim it" comment described a sweeper nobody wrote. The
-- only way to find such an object was to list the bucket.
--
-- This table is the missing retry handle: a row is written in the SAME
-- transaction that deletes the object, so the debt is durable before the
-- byte-delete is ever attempted. That is exactly the transactional outbox of
-- ADR-0003 — the same pattern this codebase already applies to events in this
-- same function — one floor down, over bytes instead of notifications.
--
-- Lifecycle of a row here: written with the object DELETE → the caller tries
-- the byte-delete immediately → on success the row is deleted and
-- paladin.object.purged is emitted in one tx → on failure it stays and the purge
-- drainer retries with backoff. Steady state is an empty table; a non-empty
-- one is a backlog worth alerting on.
CREATE TABLE IF NOT EXISTS pending_purges (
    purge_id        uuid        PRIMARY KEY,
    -- No FK to tenants, and none to objects. objects is gone by construction —
    -- that is the whole point. tenants is omitted deliberately: ON DELETE
    -- CASCADE would silently discard purge debt exactly when a tenant is
    -- purged, which is when the most bytes are at stake, and RESTRICT would
    -- let unreclaimed bytes block tenant deletion. The ids are carried as
    -- plain values so the debt outlives everything it refers to.
    tenant_id       uuid        NOT NULL,
    -- Provenance only; the objects row no longer exists. Kept so an operator
    -- can correlate a stuck purge with the audit log entry for its delete.
    object_id       uuid        NOT NULL,
    -- The full routing tuple, denormalised for the same reason: every table
    -- that could resolve it may be gone by the time the drainer runs.
    backend_id      text        NOT NULL,
    bucket_name     text        NOT NULL,
    object_key      text        NOT NULL,
    key             text        NOT NULL,
    attempts        integer     NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error      text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now()
);

-- Drainer claim: "what is due now", oldest debt first.
CREATE INDEX IF NOT EXISTS pending_purges_due_idx
    ON pending_purges (next_attempt_at);

-- Operator view: which tenant is accumulating unreclaimed bytes.
CREATE INDEX IF NOT EXISTS pending_purges_tenant_idx
    ON pending_purges (tenant_id, created_at DESC);

-- RLS, matching migration 023's treatment of every tenant-scoped table. The
-- API writes these rows inside the request transaction, where the
-- paladin.tenant_id GUC is set; the drainer runs on the worker's BYPASSRLS pool
-- because reclaiming bytes is deliberately cross-tenant.
ALTER TABLE pending_purges ENABLE ROW LEVEL SECURITY;
ALTER TABLE pending_purges FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON pending_purges;
CREATE POLICY tenant_isolation ON pending_purges
    FOR ALL
    USING (tenant_id = paladin_session_tenant_id())
    WITH CHECK (tenant_id = paladin_session_tenant_id());

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS pending_purges;
-- +goose StatementEnd
