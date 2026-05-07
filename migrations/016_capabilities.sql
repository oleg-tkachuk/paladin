-- +goose Up
-- +goose StatementBegin

-- ─── capability_records — issuance audit trail ──────────────────────────────
--
-- Every capability the cap-issuer mints lands here. The verifier does NOT
-- read this table on the hot path (signature + JWKS + revocation cache
-- check is enough), but admin tooling (`paladin cap show`, audit dashboards)
-- and the housekeeping reaper do. Storing the full claim set keeps token
-- inspection pure-SQL — no need to keep raw JWTs around.
--
-- Natural key: id (the JTI claim, UUIDv7-encouraged so per-tenant indexes
-- stay tight). The expiry-keyed BRIN index supports the reaper's "drop
-- everything expired more than N days ago" sweep.
--
-- Provenance: parent_id is the delegation chain pointer, NULLable for
-- root capabilities. generation lets a worker write predicate `(SELECT
-- generation FROM capability_records WHERE id = $1) = $2` to fence
-- writes from a stale capability after rotation.
--
-- principal_kind / principal_subject / principal_payload split lets us
-- index on the cheap (kind, subject) for ListByPrincipal and keep the
-- richer agent-specific fields in the JSONB blob without bloating every
-- row's hot path.

CREATE TABLE IF NOT EXISTS capability_records (
    id                 uuid PRIMARY KEY,
    tenant_id          uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    issuer             text NOT NULL,
    principal_kind     text NOT NULL,
    principal_subject  text NOT NULL,
    principal_payload  jsonb NOT NULL DEFAULT '{}'::jsonb,
    audience           text[] NOT NULL,
    caveats            jsonb NOT NULL,
    parent_id          uuid REFERENCES capability_records(id) ON DELETE SET NULL,
    generation         bigint NOT NULL DEFAULT 1,
    issued_at          timestamptz NOT NULL DEFAULT NOW(),
    not_before         timestamptz,
    expires_at         timestamptz NOT NULL,
    -- created_by is the actor (user UUID, agent UUID, automation
    -- principal name) who triggered issuance. Free-text because the
    -- caller's principal universe is broader than tenants.users.
    created_by         text NOT NULL DEFAULT ''
);

-- ListByPrincipal needs (tenant, kind, subject) — the typical UI view.
CREATE INDEX IF NOT EXISTS capability_records_principal_idx
    ON capability_records (tenant_id, principal_kind, principal_subject);

-- Reaper / dashboards filter by expiry. BRIN keeps the index small even
-- with hundreds of millions of rows, since issued_at / expires_at are
-- monotonically increasing.
CREATE INDEX IF NOT EXISTS capability_records_expires_brin
    ON capability_records USING brin (expires_at);

-- Cascade revoke walks the delegation tree. Hash index on parent_id
-- keeps that walk cheap.
CREATE INDEX IF NOT EXISTS capability_records_parent_idx
    ON capability_records (parent_id) WHERE parent_id IS NOT NULL;

-- ─── capability_revocations — denylist consulted by every verify ────────────
--
-- Verifiers cache this table with a short TTL (≤2s default) and gate
-- every request on it. The row count stays bounded because PurgeExpired
-- in the housekeeping worker drops entries whose underlying capability
-- has been expired for the configured grace window.
--
-- Idempotent insert: revoking the same capability twice produces one row.

CREATE TABLE IF NOT EXISTS capability_revocations (
    id            uuid PRIMARY KEY REFERENCES capability_records(id) ON DELETE CASCADE,
    revoked_at    timestamptz NOT NULL DEFAULT NOW(),
    -- Reason is an operator-supplied label (compromise / rotation /
    -- policy-change / explicit-user-action). Free text but conventionally
    -- one of a small set; queries can group by it for ops dashboards.
    reason        text NOT NULL DEFAULT '',
    -- Actor identifies who triggered the revocation. Free text for the
    -- same reason as capability_records.created_by.
    actor         text NOT NULL DEFAULT '',
    -- Cascade flag tells the reaper not to expire this entry until the
    -- whole subtree is also expired. Mirrors RevokeArgs.CascadeChildren
    -- for audit completeness.
    cascade       boolean NOT NULL DEFAULT false
);

CREATE INDEX IF NOT EXISTS capability_revocations_revoked_at_brin
    ON capability_revocations USING brin (revoked_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS capability_revocations;
DROP TABLE IF EXISTS capability_records;
-- +goose StatementEnd
