-- +goose Up
-- +goose StatementBegin

-- Counters of Biscuit copies that set request or budget limits of their own,
-- keyed by the revocation id of the block that set them. A copy and every copy
-- attenuated from it carry that id, so they count together here while
-- siblings narrowed apart count apart; the capability's own row in
-- capability_usage still takes every request and charge.
--
-- A row appears the first time such a copy is used, and goes with its
-- capability. Isolated through the capability, as capability_usage is (004):
-- reads admit the cross-tenant flag, writes stay pinned to the tenant.
--
-- A new, empty table, so its index is built in the same transaction.
CREATE TABLE capability_copy_usage (
    revocation_id bytea PRIMARY KEY,
    capability_id uuid NOT NULL REFERENCES capability_records(id) ON DELETE CASCADE,
    request_count bigint NOT NULL DEFAULT 0,
    spent         numeric(14,6) NOT NULL DEFAULT 0,
    reserved      numeric(14,6) NOT NULL DEFAULT 0,
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX capability_copy_usage_capability_idx ON capability_copy_usage (capability_id);

ALTER TABLE capability_copy_usage ENABLE ROW LEVEL SECURITY;
ALTER TABLE capability_copy_usage FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON capability_copy_usage FOR ALL
    USING (paladin_session_cross_tenant()
           OR EXISTS (SELECT 1 FROM capability_records c
                      WHERE c.id = capability_copy_usage.capability_id
                        AND c.tenant_id = paladin_session_tenant_id()))
    WITH CHECK (EXISTS (SELECT 1 FROM capability_records c
                        WHERE c.id = capability_copy_usage.capability_id
                          AND c.tenant_id = paladin_session_tenant_id()));

-- The copies a charge debited and a reservation holds against, so a refund,
-- settle or release returns to them without being handed the token again; a
-- reservation also keeps their budget limits, which settling checks. Nullable
-- with no default: a metadata change, no rewrite (CONVENTIONS.md).
ALTER TABLE charges ADD COLUMN copy_ids bytea[];
ALTER TABLE capability_reservations ADD COLUMN copy_ids bytea[];
ALTER TABLE capability_reservations ADD COLUMN copy_max_budget_micros bigint[];

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE capability_reservations DROP COLUMN IF EXISTS copy_max_budget_micros;
ALTER TABLE capability_reservations DROP COLUMN IF EXISTS copy_ids;
ALTER TABLE charges DROP COLUMN IF EXISTS copy_ids;
DROP TABLE IF EXISTS capability_copy_usage;
-- +goose StatementEnd
