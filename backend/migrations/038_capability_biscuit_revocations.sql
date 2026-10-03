-- +goose Up
-- +goose StatementBegin

-- Revoked copies of a capability's Biscuit, by block revocation id. A token
-- carrying any listed id is refused, so a row stops one copy and every copy
-- attenuated from it, while the capability itself stays live.
--
-- Isolated through the capability the copy belongs to, as
-- capability_revocations is (004): USING admits the cross-tenant flag, which
-- the verifier sets because it reads before any tenant is known; WITH CHECK
-- stays tenant-pinned, so nobody lists a copy of another tenant's capability.
--
-- A new, empty table, so its index is built in the same transaction.
CREATE TABLE capability_biscuit_revocations (
    revocation_id bytea PRIMARY KEY,
    capability_id uuid NOT NULL REFERENCES capability_records(id) ON DELETE CASCADE,
    revoked_at    timestamptz NOT NULL DEFAULT now(),
    reason        text NOT NULL DEFAULT '',
    actor         text NOT NULL DEFAULT ''
);
CREATE INDEX capability_biscuit_revocations_capability_idx
    ON capability_biscuit_revocations (capability_id);

ALTER TABLE capability_biscuit_revocations ENABLE ROW LEVEL SECURITY;
ALTER TABLE capability_biscuit_revocations FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON capability_biscuit_revocations FOR ALL
    USING (paladin_session_cross_tenant()
           OR EXISTS (SELECT 1 FROM capability_records c
                      WHERE c.id = capability_biscuit_revocations.capability_id
                        AND c.tenant_id = paladin_session_tenant_id()))
    WITH CHECK (EXISTS (SELECT 1 FROM capability_records c
                        WHERE c.id = capability_biscuit_revocations.capability_id
                          AND c.tenant_id = paladin_session_tenant_id()));

-- Announced on the channel 033 set up, so every replica's caches clear.
CREATE TRIGGER capability_biscuit_revocations_notify
    AFTER INSERT ON capability_biscuit_revocations
    FOR EACH STATEMENT EXECUTE FUNCTION paladin_notify_capability_revoked();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS capability_biscuit_revocations;
-- +goose StatementEnd
