-- +goose Up
-- +goose StatementBegin

-- ─── 006: control/data/iam plane split ────────────────────────────────────────
--
-- Adds first-class IAM (users, api_keys, refresh_tokens), audit log,
-- per-tenant event subscriptions, per-tenant/bucket quotas, object versioning,
-- and Object Lock (WORM). Promotes storage_backends + buckets to fully
-- DB-managed entities (no longer config-only audit shadows).
--
-- No backward compatibility shims — this is a v2 schema. Existing data
-- shape is preserved where compatible; new columns get sensible defaults.

-- ─── storage_backends: promote to first-class ────────────────────────────────
ALTER TABLE storage_backends
    ADD COLUMN IF NOT EXISTS display_name            TEXT,
    ADD COLUMN IF NOT EXISTS public_endpoint         TEXT,
    ADD COLUMN IF NOT EXISTS force_path_style        BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS credentials_secret_ref  TEXT,
    ADD COLUMN IF NOT EXISTS sse_type                TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS sse_key_id              TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS events_queue_url        TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS events_poll_interval_ms BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cedar_policy            TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS cedar_policy_hash       BYTEA,
    ADD COLUMN IF NOT EXISTS resource_version        BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS updated_at              TIMESTAMPTZ NOT NULL DEFAULT now();

DROP TRIGGER IF EXISTS trg_storage_backends_bump_rv ON storage_backends;
CREATE TRIGGER trg_storage_backends_bump_rv
    BEFORE UPDATE ON storage_backends
    FOR EACH ROW EXECUTE FUNCTION bump_resource_version();

-- ─── buckets: add owner_tenant_id, policy, constraints, lock, versioning ─────
ALTER TABLE buckets
    ADD COLUMN IF NOT EXISTS owner_tenant_id        UUID REFERENCES tenants(tenant_id) ON DELETE RESTRICT,
    ADD COLUMN IF NOT EXISTS cedar_policy           TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS cedar_policy_hash      BYTEA,
    ADD COLUMN IF NOT EXISTS constraints            JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS lifecycle_rules        JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS object_lock_enabled    BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS object_lock_default_mode TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS object_lock_default_retention_seconds BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS versioning_enabled     BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS versioning_keep_deletes_forever BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS replication_enabled    BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS replication_destination TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS replication_filter     TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_buckets_owner_tenant
    ON buckets(owner_tenant_id) WHERE owner_tenant_id IS NOT NULL;

-- enforce_object_key_bucket_tenancy: when an object_key is bound to a bucket
-- whose owner_tenant_id is set, the object_key must belong to the same
-- tenant. Shared (NULL owner) buckets remain bindable from any tenant.
CREATE OR REPLACE FUNCTION enforce_object_key_bucket_tenancy() RETURNS TRIGGER AS $$
DECLARE
    bucket_owner UUID;
BEGIN
    SELECT owner_tenant_id INTO bucket_owner
    FROM buckets
    WHERE backend_id = NEW.backend_id AND bucket_name = NEW.bucket_name;

    IF bucket_owner IS NOT NULL AND bucket_owner <> NEW.tenant_id THEN
        RAISE EXCEPTION 'object_key tenant_id % cannot bind to bucket owned by tenant %',
            NEW.tenant_id, bucket_owner USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_object_keys_enforce_bucket_tenancy ON object_keys;
CREATE TRIGGER trg_object_keys_enforce_bucket_tenancy
    BEFORE INSERT OR UPDATE OF bucket_name, backend_id, tenant_id ON object_keys
    FOR EACH ROW EXECUTE FUNCTION enforce_object_key_bucket_tenancy();

-- ─── object_keys: per-key constraints overlay ────────────────────────────────
ALTER TABLE object_keys
    ADD COLUMN IF NOT EXISTS constraints JSONB NOT NULL DEFAULT '{}'::jsonb;

-- ─── objects: lock + version pointer ─────────────────────────────────────────
ALTER TABLE objects
    ADD COLUMN IF NOT EXISTS current_version_id  UUID,
    ADD COLUMN IF NOT EXISTS lock_mode           TEXT NOT NULL DEFAULT '', -- 'GOVERNANCE' | 'COMPLIANCE' | ''
    ADD COLUMN IF NOT EXISTS lock_retain_until   TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS legal_hold          BOOLEAN NOT NULL DEFAULT FALSE;

-- ─── object_versions: immutable history (opt-in per bucket) ──────────────────
CREATE TABLE IF NOT EXISTS object_versions (
    version_id            UUID PRIMARY KEY,         -- UUIDv7
    object_id             UUID NOT NULL REFERENCES objects(object_id) ON DELETE CASCADE,
    is_delete_marker      BOOLEAN NOT NULL DEFAULT FALSE,
    s3_key                TEXT NOT NULL,            -- composed: "<base_key>.v<version_id>" or backend-native versionId
    size_bytes            BIGINT,
    etag                  TEXT,
    checksum_algorithm    SMALLINT NOT NULL DEFAULT 0,
    checksum              TEXT,
    content_type          TEXT,
    metadata              JSONB NOT NULL DEFAULT '{}'::jsonb,
    tags                  JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- Per-version Object Lock fields. Once set, blocked from removal until expiry.
    lock_mode             TEXT NOT NULL DEFAULT '',
    lock_retain_until     TIMESTAMPTZ,
    legal_hold            BOOLEAN NOT NULL DEFAULT FALSE,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_object_versions_object_id_created
    ON object_versions(object_id, created_at DESC);

-- enforce_version_lock: block UPDATE/DELETE on locked rows. Compliance mode
-- is non-bypassable; governance mode honors a session GUC `paladin.governance_bypass`.
CREATE OR REPLACE FUNCTION enforce_object_version_lock() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' OR (TG_OP = 'UPDATE' AND OLD.legal_hold IS DISTINCT FROM NEW.legal_hold) THEN
        IF OLD.legal_hold THEN
            RAISE EXCEPTION 'object version % is under legal hold', OLD.version_id
                USING ERRCODE = 'check_violation';
        END IF;
        IF OLD.lock_mode = 'COMPLIANCE' AND OLD.lock_retain_until IS NOT NULL AND OLD.lock_retain_until > now() THEN
            RAISE EXCEPTION 'compliance lock active on version % until %', OLD.version_id, OLD.lock_retain_until
                USING ERRCODE = 'check_violation';
        END IF;
        IF OLD.lock_mode = 'GOVERNANCE' AND OLD.lock_retain_until IS NOT NULL AND OLD.lock_retain_until > now() THEN
            IF NOT COALESCE(current_setting('paladin.governance_bypass', true)::boolean, false) THEN
                RAISE EXCEPTION 'governance lock active on version % until %', OLD.version_id, OLD.lock_retain_until
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;
    END IF;
    RETURN COALESCE(NEW, OLD);
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_object_versions_enforce_lock ON object_versions;
CREATE TRIGGER trg_object_versions_enforce_lock
    BEFORE UPDATE OR DELETE ON object_versions
    FOR EACH ROW EXECUTE FUNCTION enforce_object_version_lock();

-- ─── IAM: users ──────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS users (
    user_id          UUID PRIMARY KEY,                   -- UUIDv7
    tenant_id        UUID NOT NULL REFERENCES tenants(tenant_id) ON DELETE RESTRICT,
    subject          TEXT NOT NULL,                      -- email / external sub; unique per tenant
    display_name     TEXT,
    password_hash    BYTEA,                              -- bcrypt; NULL for federated
    roles            JSONB NOT NULL DEFAULT '[]'::jsonb, -- array<string>
    scopes           JSONB NOT NULL DEFAULT '[]'::jsonb, -- array<string> (wire-form scopes)
    disabled         BOOLEAN NOT NULL DEFAULT FALSE,
    resource_version BIGINT NOT NULL DEFAULT 1,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at    TIMESTAMPTZ,
    UNIQUE (tenant_id, subject)
);

CREATE INDEX IF NOT EXISTS idx_users_tenant ON users(tenant_id);

DROP TRIGGER IF EXISTS trg_users_bump_rv ON users;
CREATE TRIGGER trg_users_bump_rv
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION bump_resource_version();

-- ─── IAM: api_keys ───────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS api_keys (
    api_key_id        UUID PRIMARY KEY,
    tenant_id         UUID REFERENCES tenants(tenant_id) ON DELETE RESTRICT, -- NULL = platform-level
    display_prefix    TEXT NOT NULL,                    -- first 8 chars of secret, shown in UI
    description       TEXT NOT NULL DEFAULT '',
    secret_hash       BYTEA NOT NULL,
    -- During rotation the previous secret remains valid until secret_hash_old_until.
    secret_hash_old   BYTEA,
    secret_hash_old_until TIMESTAMPTZ,
    roles             JSONB NOT NULL DEFAULT '[]'::jsonb,
    scopes            JSONB NOT NULL DEFAULT '[]'::jsonb,
    revoked           BOOLEAN NOT NULL DEFAULT FALSE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at        TIMESTAMPTZ,
    last_used_at      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_api_keys_tenant ON api_keys(tenant_id) WHERE tenant_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_api_keys_prefix ON api_keys(display_prefix);

-- ─── IAM: refresh_tokens (revocation list) ───────────────────────────────────
CREATE TABLE IF NOT EXISTS refresh_tokens (
    jti        UUID PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users(user_id) ON DELETE CASCADE,
    tenant_id  UUID NOT NULL,
    issued_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked    BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user ON refresh_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_expiry ON refresh_tokens(expires_at);

-- ─── Audit log (append-only) ─────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS audit_log (
    entry_id        UUID PRIMARY KEY,                   -- UUIDv7
    at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_subject   TEXT NOT NULL,
    actor_tenant_id UUID,
    actor_audience  TEXT NOT NULL,
    action          TEXT NOT NULL,                      -- "admin.BucketService.CreateBucket"
    resource_name   TEXT NOT NULL,
    request_id      TEXT,
    source_ip       TEXT,
    before_json     BYTEA,
    after_json      BYTEA,
    error_message   TEXT
);

CREATE INDEX IF NOT EXISTS idx_audit_log_at ON audit_log(at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_log_actor ON audit_log(actor_subject, at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_log_resource ON audit_log(resource_name, at DESC);

-- ─── Quota ───────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS quotas (
    -- Composite key: tenant-scope (tenant_id,'','') or bucket-scope
    -- (NULL, backend_id, bucket_name). Exactly one of these forms must be set.
    quota_id              UUID PRIMARY KEY,
    tenant_id             UUID REFERENCES tenants(tenant_id) ON DELETE CASCADE,
    backend_id            TEXT,
    bucket_name           TEXT,
    max_total_bytes       BIGINT NOT NULL DEFAULT 0,
    max_object_count      BIGINT NOT NULL DEFAULT 0,
    max_bytes_per_day     BIGINT NOT NULL DEFAULT 0,
    max_objects_per_day   BIGINT NOT NULL DEFAULT 0,
    -- Usage counters maintained by the accounting worker.
    usage_total_bytes     BIGINT NOT NULL DEFAULT 0,
    usage_object_count    BIGINT NOT NULL DEFAULT 0,
    usage_bytes_today     BIGINT NOT NULL DEFAULT 0,
    usage_objects_today   BIGINT NOT NULL DEFAULT 0,
    last_reset_at         TIMESTAMPTZ,
    resource_version      BIGINT NOT NULL DEFAULT 1,
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Enforce exclusivity via composite unique indexes.
    CHECK ((tenant_id IS NOT NULL AND backend_id IS NULL AND bucket_name IS NULL)
        OR (tenant_id IS NULL AND backend_id IS NOT NULL AND bucket_name IS NOT NULL)),
    -- Bucket FK validated via composite reference.
    FOREIGN KEY (backend_id, bucket_name) REFERENCES buckets(backend_id, bucket_name) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS ux_quotas_tenant ON quotas(tenant_id) WHERE tenant_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS ux_quotas_bucket ON quotas(backend_id, bucket_name) WHERE backend_id IS NOT NULL;

DROP TRIGGER IF EXISTS trg_quotas_bump_rv ON quotas;
CREATE TRIGGER trg_quotas_bump_rv
    BEFORE UPDATE ON quotas
    FOR EACH ROW EXECUTE FUNCTION bump_resource_version();

-- ─── Event subscriptions ────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS event_subscriptions (
    subscription_id  UUID PRIMARY KEY,
    tenant_id        UUID NOT NULL REFERENCES tenants(tenant_id) ON DELETE CASCADE,
    cel_filter       TEXT NOT NULL DEFAULT '',
    sink_kind        TEXT NOT NULL,                     -- 'http' | 'kafka' | 'sqs'
    sink_config      JSONB NOT NULL,                    -- shape varies by kind
    disabled         BOOLEAN NOT NULL DEFAULT FALSE,
    resource_version BIGINT NOT NULL DEFAULT 1,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_event_subscriptions_tenant ON event_subscriptions(tenant_id);

DROP TRIGGER IF EXISTS trg_event_subscriptions_bump_rv ON event_subscriptions;
CREATE TRIGGER trg_event_subscriptions_bump_rv
    BEFORE UPDATE ON event_subscriptions
    FOR EACH ROW EXECUTE FUNCTION bump_resource_version();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS event_subscriptions;
DROP TABLE IF EXISTS quotas;
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS users;
DROP TRIGGER IF EXISTS trg_object_versions_enforce_lock ON object_versions;
DROP FUNCTION IF EXISTS enforce_object_version_lock;
DROP TABLE IF EXISTS object_versions;
ALTER TABLE objects
    DROP COLUMN IF EXISTS legal_hold,
    DROP COLUMN IF EXISTS lock_retain_until,
    DROP COLUMN IF EXISTS lock_mode,
    DROP COLUMN IF EXISTS current_version_id;
ALTER TABLE object_keys DROP COLUMN IF EXISTS constraints;
DROP TRIGGER IF EXISTS trg_object_keys_enforce_bucket_tenancy ON object_keys;
DROP FUNCTION IF EXISTS enforce_object_key_bucket_tenancy;
ALTER TABLE buckets
    DROP COLUMN IF EXISTS replication_filter,
    DROP COLUMN IF EXISTS replication_destination,
    DROP COLUMN IF EXISTS replication_enabled,
    DROP COLUMN IF EXISTS versioning_keep_deletes_forever,
    DROP COLUMN IF EXISTS versioning_enabled,
    DROP COLUMN IF EXISTS object_lock_default_retention_seconds,
    DROP COLUMN IF EXISTS object_lock_default_mode,
    DROP COLUMN IF EXISTS object_lock_enabled,
    DROP COLUMN IF EXISTS lifecycle_rules,
    DROP COLUMN IF EXISTS constraints,
    DROP COLUMN IF EXISTS cedar_policy_hash,
    DROP COLUMN IF EXISTS cedar_policy,
    DROP COLUMN IF EXISTS owner_tenant_id;
DROP TRIGGER IF EXISTS trg_storage_backends_bump_rv ON storage_backends;
ALTER TABLE storage_backends
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS resource_version,
    DROP COLUMN IF EXISTS cedar_policy_hash,
    DROP COLUMN IF EXISTS cedar_policy,
    DROP COLUMN IF EXISTS events_poll_interval_ms,
    DROP COLUMN IF EXISTS events_queue_url,
    DROP COLUMN IF EXISTS sse_key_id,
    DROP COLUMN IF EXISTS sse_type,
    DROP COLUMN IF EXISTS credentials_secret_ref,
    DROP COLUMN IF EXISTS force_path_style,
    DROP COLUMN IF EXISTS public_endpoint,
    DROP COLUMN IF EXISTS display_name;
-- +goose StatementEnd
