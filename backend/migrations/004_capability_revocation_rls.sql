-- +goose Up
-- +goose StatementBegin

-- capability_revocations had no row-level security, and Store.Revoke took the
-- capability id from the caller without checking who owned it. The two gaps
-- compose into a cross-tenant denial of service: any tenant holding — or
-- guessing — another tenant's capability id could revoke it, and the revocation
-- would be honoured by every verifier immediately.
--
-- The table carries no tenant_id of its own; its id IS a capability_records id.
-- So isolation comes from the capability the revocation points at, the same
-- shape capability_usage already uses.
--
-- The cascade path was already safe by accident (its recursive CTE reads
-- capability_records, which RLS filters). The single-revoke path was not.
-- Fixing it in SQL rather than only in Go closes the class instead of the
-- instance: any future writer of this table inherits the boundary.

ALTER TABLE capability_revocations ENABLE ROW LEVEL SECURITY;
ALTER TABLE capability_revocations FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation ON capability_revocations;
CREATE POLICY tenant_isolation ON capability_revocations FOR ALL
    USING (paladin_session_cross_tenant()
           OR EXISTS (SELECT 1 FROM capability_records c
                      WHERE c.id = capability_revocations.id
                        AND c.tenant_id = paladin_session_tenant_id()))
    WITH CHECK (EXISTS (SELECT 1 FROM capability_records c
                        WHERE c.id = capability_revocations.id
                          AND c.tenant_id = paladin_session_tenant_id()));

-- capability_usage predates the cross-tenant escape hatch, so its USING clause
-- has no way to admit a caller that legitimately spans tenants. Two such
-- callers exist: the admin plane reading another tenant's spend, and the
-- background purger, which runs with no request principal at all and therefore
-- no session tenant. Without this, PurgeOrphans matches zero rows on every
-- pass and the table grows without bound while reporting success.
--
-- USING only. WITH CHECK stays tenant-pinned: the hatch widens what a caller
-- can READ or REMOVE, never what it can write into another tenant's ledger.
DROP POLICY IF EXISTS tenant_isolation ON capability_usage;
CREATE POLICY tenant_isolation ON capability_usage FOR ALL
    USING (paladin_session_cross_tenant()
           OR EXISTS (SELECT 1 FROM capability_records c
                      WHERE c.id = capability_usage.capability_id
                        AND c.tenant_id = paladin_session_tenant_id()))
    WITH CHECK (EXISTS (SELECT 1 FROM capability_records c
                        WHERE c.id = capability_usage.capability_id
                          AND c.tenant_id = paladin_session_tenant_id()));

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DROP POLICY IF EXISTS tenant_isolation ON capability_revocations;
ALTER TABLE capability_revocations DISABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
