-- +goose Up
-- +goose StatementBegin

-- Paladin baseline schema.
--
-- This is a CONSOLIDATED baseline: it replaces the 65 incremental migrations
-- that built the schema between 2026-05 and 2026-08. Those migrations are in
-- git history; they are not carried forward because every deployment
-- reprovisions (ADR-0013), so no database will ever replay them.
--
-- Conventions, all of them from ADR-0013 — read it before adding a table:
--
--   * `id` is the primary key, always, and it is a uuid. A column named
--     `<entity>_id` is a FOREIGN key to `<entity>.id` and nothing else.
--     Append-only logs may use `bigint GENERATED ALWAYS AS IDENTITY` instead,
--     where a uuid buys nothing and costs index width.
--   * Natural uniqueness is a UNIQUE constraint, never the identity.
--   * `name` is the natural name, unique within its parent. `display_name` is
--     the human label and carries no uniqueness.
--   * States are Postgres ENUMs, never text + CHECK.
--   * `tenant_id` is denormalised onto every tenant-scoped table so RLS
--     policies stay single-table. Where a child also references a parent that
--     carries a tenant, a composite FK pins them together so a row cannot
--     claim a parent from another tenant.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ─── Enumerated states ──────────────────────────────────────────────────────

CREATE TYPE object_state AS ENUM ('PENDING', 'AVAILABLE', 'FAILED', 'DELETED');

CREATE TYPE operation_state AS ENUM (
    'PENDING', 'RUNNING', 'SUCCEEDED', 'FAILED', 'CANCELLED');

-- Was text + CHECK on two tables (ADR-0013 §5).
CREATE TYPE object_lock_mode AS ENUM ('GOVERNANCE', 'COMPLIANCE');

CREATE TYPE backend_health_status AS ENUM ('unknown', 'ok', 'error');

CREATE TYPE event_sink_kind AS ENUM ('http', 'nats', 'kafka', 'sqs');

CREATE TYPE tenant_storage_layout AS ENUM ('shared', 'dedicated');

-- ─── Tenancy ────────────────────────────────────────────────────────────────

CREATE TABLE tenants (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug                   text NOT NULL,
    display_name           text NOT NULL,
    labels                 jsonb NOT NULL DEFAULT '{}'::jsonb,
    inherited_cedar_policy text NOT NULL DEFAULT '',
    inherited_policy_hash  bytea,
    storage_layout         tenant_storage_layout NOT NULL DEFAULT 'shared',
    resource_version       bigint NOT NULL DEFAULT 1,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    deleted_at             timestamptz,
    CONSTRAINT tenants_slug_format
        CHECK (slug ~ '^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?$'),
    CONSTRAINT tenants_display_name_format
        CHECK (char_length(display_name) BETWEEN 1 AND 255)
);

-- Slug is unique among LIVE tenants only: a soft-deleted tenant keeps its slug
-- in the row (for restore) but must not block a new tenant from taking it.
CREATE UNIQUE INDEX tenants_slug_live_key
    ON tenants (slug) WHERE deleted_at IS NULL;

-- Append-only: bigint identity, not uuid (ADR-0013).
CREATE TABLE tenant_slug_history (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    old_slug   text NOT NULL,
    new_slug   text NOT NULL,
    renamed_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX tenant_slug_history_tenant_idx ON tenant_slug_history (tenant_id);
CREATE INDEX tenant_slug_history_old_slug_idx ON tenant_slug_history (old_slug);

-- ─── Users and human authentication ─────────────────────────────────────────

CREATE TABLE users (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    subject          text NOT NULL,
    display_name     text,
    password_hash    bytea,
    roles            jsonb NOT NULL DEFAULT '[]'::jsonb,
    scopes           jsonb NOT NULL DEFAULT '[]'::jsonb,
    disabled         boolean NOT NULL DEFAULT false,
    resource_version bigint NOT NULL DEFAULT 1,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    last_login_at    timestamptz,
    UNIQUE (tenant_id, subject),
    -- Target for user_settings' composite FK: replaces the
    -- enforce_user_settings_tenant trigger with a declarative constraint.
    UNIQUE (tenant_id, id)
) WITH (fillfactor = '85');

CREATE TABLE user_settings (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          uuid NOT NULL UNIQUE,
    tenant_id        uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    timezone         text NOT NULL DEFAULT 'UTC',
    locale           text NOT NULL DEFAULT 'en-US',
    theme            text NOT NULL DEFAULT 'system',
    preferences      jsonb NOT NULL DEFAULT '{}'::jsonb,
    resource_version bigint NOT NULL DEFAULT 1,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT user_settings_locale_format
        CHECK (length(locale) BETWEEN 2 AND 35),
    CONSTRAINT user_settings_theme_check
        CHECK (theme IN ('system', 'light', 'dark')),
    FOREIGN KEY (tenant_id, user_id)
        REFERENCES users (tenant_id, id) ON DELETE CASCADE
);

-- `id` IS the JWT `jti`. Named `id` per the convention; the claim it populates
-- is documented here rather than encoded in the column name.
CREATE TABLE refresh_tokens (
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    family_id  uuid NOT NULL DEFAULT gen_random_uuid(),
    issued_at  timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    revoked    boolean NOT NULL DEFAULT false
);
CREATE INDEX refresh_tokens_user_idx ON refresh_tokens (user_id);
CREATE INDEX refresh_tokens_family_idx ON refresh_tokens (family_id);
CREATE INDEX refresh_tokens_expiry_idx ON refresh_tokens (expires_at);

-- ─── Machine authentication ─────────────────────────────────────────────────

CREATE TABLE api_tokens (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name           text NOT NULL,
    prefix         text NOT NULL,
    token_hmac     bytea,
    scopes         text[] NOT NULL DEFAULT '{}',
    roles          text[] NOT NULL DEFAULT '{}',
    audience       text[] NOT NULL,
    rate_limit_rpm integer NOT NULL DEFAULT 0,
    expires_at     timestamptz NOT NULL,
    revoked_at     timestamptz,
    last_used_at   timestamptz,
    created_by     text NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT api_tokens_rate_limit_rpm_check CHECK (rate_limit_rpm >= 0)
);
CREATE INDEX api_tokens_tenant_idx ON api_tokens (tenant_id);
-- Lookup is by prefix, then HMAC verify. Prefix is not unique by construction,
-- so this is a plain index and the verifier resolves collisions.
CREATE INDEX api_tokens_prefix_idx ON api_tokens (prefix);

CREATE TABLE api_token_rate_buckets (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    token_id     uuid NOT NULL REFERENCES api_tokens(id) ON DELETE CASCADE,
    bucket_start timestamptz NOT NULL,
    count        bigint NOT NULL DEFAULT 0,
    UNIQUE (token_id, bucket_start)
);

-- ─── OAuth 2.1 authorization server ─────────────────────────────────────────

-- `client_id` is the ONE naming exception in the schema (ADR-0013 §2): a
-- public OAuth protocol identifier that appears in issued tokens and in client
-- configuration, so it is an external contract rather than our identity.
CREATE TABLE oauth_clients (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id         text NOT NULL UNIQUE,
    client_name       text NOT NULL DEFAULT '',
    redirect_uris     text[] NOT NULL DEFAULT '{}',
    allowed_scopes    text[] NOT NULL DEFAULT '{}',
    allowed_audiences text[] NOT NULL DEFAULT '{}',
    secret_hash       bytea,
    is_public         boolean NOT NULL DEFAULT true,
    created_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE oauth_authorization_codes (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code_hash             bytea NOT NULL UNIQUE,
    oauth_client_id       uuid NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
    user_id               uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id             uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    redirect_uri          text NOT NULL,
    code_challenge        text NOT NULL,
    code_challenge_method text NOT NULL,
    scopes                text[] NOT NULL DEFAULT '{}',
    audience              text NOT NULL,
    expires_at            timestamptz NOT NULL,
    consumed_at           timestamptz,
    created_at            timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX oauth_authorization_codes_expiry_idx
    ON oauth_authorization_codes (expires_at);

-- ─── Physical storage ───────────────────────────────────────────────────────

-- `name` holds what used to be the primary key: the config key under
-- `storage.backends.*` ("primary", "secondary"). Bootstrap resolves name → id.
CREATE TABLE storage_backends (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                    text NOT NULL UNIQUE,
    kind                    text NOT NULL,
    provider                text NOT NULL DEFAULT '',
    display_name            text,
    endpoint                text,
    public_endpoint         text,
    region                  text,
    force_path_style        boolean NOT NULL DEFAULT false,
    credentials_secret_ref  text,
    previous_credentials_secret_ref  text,
    previous_credentials_valid_until timestamptz,
    sse_type                text NOT NULL DEFAULT '',
    sse_key_id              text,
    events_enabled          boolean NOT NULL DEFAULT false,
    events_target           text,
    events_queue_url        text,
    events_poll_interval_ms bigint NOT NULL DEFAULT 20000,
    cedar_policy            text NOT NULL DEFAULT '',
    cedar_policy_hash       bytea,
    enabled                 boolean NOT NULL DEFAULT true,
    read_only               boolean NOT NULL DEFAULT false,
    maintenance             boolean NOT NULL DEFAULT false,
    resource_version        bigint NOT NULL DEFAULT 1,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE storage_backend_health (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    backend_id uuid NOT NULL UNIQUE REFERENCES storage_backends(id) ON DELETE CASCADE,
    status     backend_health_status NOT NULL DEFAULT 'unknown',
    message    text,
    checked_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE buckets (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    backend_id             uuid NOT NULL REFERENCES storage_backends(id) ON DELETE RESTRICT,
    name                   text NOT NULL,
    display_name           text,
    owner_tenant_id        uuid REFERENCES tenants(id) ON DELETE RESTRICT,
    region                 text,
    labels                 jsonb NOT NULL DEFAULT '{}'::jsonb,
    constraints            jsonb NOT NULL DEFAULT '{}'::jsonb,
    lifecycle_rules        jsonb NOT NULL DEFAULT '[]'::jsonb,
    cedar_policy           text NOT NULL DEFAULT '',
    cedar_policy_hash      bytea,
    object_lock_enabled    boolean NOT NULL DEFAULT false,
    object_lock_default_mode object_lock_mode,
    object_lock_default_retention_seconds bigint NOT NULL DEFAULT 0,
    versioning_enabled     boolean NOT NULL DEFAULT false,
    versioning_keep_deletes_forever boolean NOT NULL DEFAULT false,
    replication_enabled    boolean NOT NULL DEFAULT false,
    resource_version       bigint NOT NULL DEFAULT 1,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    UNIQUE (backend_id, name)
);
CREATE INDEX buckets_owner_tenant_idx ON buckets (owner_tenant_id);

CREATE TABLE replication_state (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    bucket_id  uuid NOT NULL UNIQUE REFERENCES buckets(id) ON DELETE CASCADE,
    watermark  timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- ─── Collections (was: collections) ─────────────────────────────────────────

-- A collection is a policy-bearing container of objects inside a tenant. It is
-- NOT a key — that naming is what ADR-0013 §3 exists to fix.
CREATE TABLE collections (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    name              text NOT NULL,
    display_name      text,
    bucket_id         uuid NOT NULL REFERENCES buckets(id) ON DELETE RESTRICT,
    constraints       jsonb NOT NULL DEFAULT '{}'::jsonb,
    lifecycle_rules   jsonb NOT NULL DEFAULT '[]'::jsonb,
    cedar_policy      text NOT NULL DEFAULT '',
    cedar_policy_hash bytea,
    resource_version  bigint NOT NULL DEFAULT 1,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name),
    -- Target of the composite FK from objects: pins an object's collection to
    -- the object's own tenant (ADR-0013 §4).
    UNIQUE (tenant_id, id),
    CONSTRAINT collections_name_format CHECK (
        char_length(name) <= 255 AND
        name ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(/[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$')
);
CREATE INDEX collections_bucket_idx ON collections (bucket_id);

CREATE TABLE tenant_default_bindings (
    id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL UNIQUE REFERENCES tenants(id) ON DELETE CASCADE,
    bucket_id uuid NOT NULL REFERENCES buckets(id) ON DELETE RESTRICT,
    set_at    timestamptz NOT NULL DEFAULT now(),
    set_by    text NOT NULL DEFAULT ''
);

CREATE TABLE tenant_storage_migrations (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    source_bucket_id   uuid NOT NULL REFERENCES buckets(id) ON DELETE RESTRICT,
    target_bucket_id   uuid NOT NULL REFERENCES buckets(id) ON DELETE RESTRICT,
    state              text NOT NULL DEFAULT 'PENDING',
    objects_total      bigint NOT NULL DEFAULT 0,
    objects_copied     bigint NOT NULL DEFAULT 0,
    cursor_collection  text,
    cursor_path        text,
    error              text,
    attempts           integer NOT NULL DEFAULT 0,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    completed_at       timestamptz
);
CREATE INDEX tenant_storage_migrations_tenant_idx
    ON tenant_storage_migrations (tenant_id);

-- ─── Objects ────────────────────────────────────────────────────────────────

CREATE TABLE objects (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          uuid NOT NULL,
    collection_id      uuid NOT NULL,
    -- The key inside the collection. `storage_path` on object_versions is the
    -- composed backend location; this is the logical one.
    path               text NOT NULL,
    state              object_state NOT NULL,
    content_type       text NOT NULL,
    size_bytes         bigint,
    etag               text,
    checksum_algorithm smallint NOT NULL DEFAULT 0,
    checksum           text,
    sequencer          text,
    metadata           jsonb NOT NULL DEFAULT '{}'::jsonb,
    tags               jsonb NOT NULL DEFAULT '{}'::jsonb,
    external_ref       text,
    current_version_id uuid,
    resource_version   bigint NOT NULL DEFAULT 1,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    committed_at       timestamptz,
    terminated_at      timestamptz,
    presign_expires_at timestamptz,
    -- Composite FK, not two separate ones: an object cannot reference a
    -- collection that belongs to a different tenant (ADR-0013 §4).
    FOREIGN KEY (tenant_id, collection_id)
        REFERENCES collections (tenant_id, id) ON DELETE RESTRICT,
    UNIQUE (collection_id, path),
    -- Target for object_versions' composite FK.
    UNIQUE (tenant_id, id)
);
CREATE INDEX objects_tenant_state_idx ON objects (tenant_id, state);
CREATE INDEX objects_collection_idx ON objects (collection_id);
CREATE INDEX objects_list_keyset_idx
    ON objects (collection_id, path, id) WHERE state <> 'DELETED';
CREATE INDEX objects_hard_delete_idx
    ON objects (terminated_at) WHERE state = 'DELETED';
CREATE INDEX objects_presign_expiry_idx
    ON objects (presign_expires_at) WHERE state = 'PENDING';

CREATE TABLE object_versions (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          uuid NOT NULL,
    object_id          uuid NOT NULL,
    is_delete_marker   boolean NOT NULL DEFAULT false,
    -- Composed backend location: "<tenant>/<collection>/<path>".
    storage_path       text NOT NULL,
    size_bytes         bigint,
    etag               text,
    checksum_algorithm smallint NOT NULL DEFAULT 0,
    checksum           text,
    content_type       text,
    metadata           jsonb NOT NULL DEFAULT '{}'::jsonb,
    tags               jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at         timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, object_id)
        REFERENCES objects (tenant_id, id) ON DELETE CASCADE,
    UNIQUE (object_id, id)
);
CREATE INDEX object_versions_object_created_idx
    ON object_versions (object_id, created_at DESC);

-- The back-pointer objects.current_version_id had NO foreign key before this
-- baseline, so a row could name a version of a different object, or a version
-- that no longer existed. It is now a real reference, DEFERRABLE because the
-- object and its first version are inserted in the same transaction, and
-- composite so the version must belong to THIS object.
ALTER TABLE objects
    ADD CONSTRAINT objects_current_version_fkey
    FOREIGN KEY (id, current_version_id)
    REFERENCES object_versions (object_id, id)
    DEFERRABLE INITIALLY DEFERRED;

-- Object Lock, lifted out of objects/object_versions where it lived as three
-- duplicated columns on each. S3 attaches retention to a version, so the
-- version owns it (ADR-0013 consequences).
CREATE TABLE object_locks (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    version_id   uuid NOT NULL UNIQUE REFERENCES object_versions(id) ON DELETE CASCADE,
    mode         object_lock_mode,
    retain_until timestamptz,
    legal_hold   boolean NOT NULL DEFAULT false,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    -- A lock row that asserts nothing is a bug, not a valid state.
    CONSTRAINT object_locks_asserts_something
        CHECK (mode IS NOT NULL OR legal_hold),
    CONSTRAINT object_locks_retention_needs_mode
        CHECK ((mode IS NULL) = (retain_until IS NULL))
);
CREATE INDEX object_locks_retain_idx ON object_locks (retain_until)
    WHERE retain_until IS NOT NULL;

CREATE TABLE object_tags (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    slug             text NOT NULL,
    display_name     text,
    description      text,
    labels           jsonb NOT NULL DEFAULT '{}'::jsonb,
    resource_version bigint NOT NULL DEFAULT 1,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, slug),
    CONSTRAINT object_tag_slug_format
        CHECK (slug ~ '^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?$')
);

-- ─── Multipart upload ───────────────────────────────────────────────────────

CREATE TABLE multipart_uploads (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    object_id         uuid NOT NULL REFERENCES objects(id) ON DELETE CASCADE,
    bucket_id         uuid NOT NULL REFERENCES buckets(id) ON DELETE RESTRICT,
    -- The backend's own upload handle. Opaque to us, hence text.
    storage_upload_id text NOT NULL,
    part_size_bytes   bigint NOT NULL,
    total_parts       integer NOT NULL,
    client_id         text NOT NULL,
    user_id           uuid NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX multipart_uploads_object_idx ON multipart_uploads (object_id);
CREATE INDEX multipart_uploads_reaper_idx ON multipart_uploads (created_at);

CREATE TABLE multipart_parts (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    upload_id   uuid NOT NULL REFERENCES multipart_uploads(id) ON DELETE CASCADE,
    part_number integer NOT NULL,
    size_bytes  bigint NOT NULL,
    etag        text NOT NULL,
    checksum    text,
    uploaded_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (upload_id, part_number)
);

-- Work queue for bytes whose row is already gone.
CREATE TABLE pending_purges (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid NOT NULL,
    object_id       uuid NOT NULL,
    bucket_id       uuid NOT NULL REFERENCES buckets(id) ON DELETE RESTRICT,
    -- Denormalised on purpose: the object row is deleted before the purge runs,
    -- so the location cannot be resolved by join at execution time.
    storage_path    text NOT NULL,
    attempts        integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error      text,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX pending_purges_due_idx ON pending_purges (next_attempt_at);

-- ─── Quotas ─────────────────────────────────────────────────────────────────

CREATE TABLE quotas (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           uuid REFERENCES tenants(id) ON DELETE CASCADE,
    bucket_id           uuid REFERENCES buckets(id) ON DELETE CASCADE,
    max_total_bytes     bigint,
    max_object_count    bigint,
    max_bytes_per_day   bigint,
    max_objects_per_day bigint,
    usage_total_bytes   bigint NOT NULL DEFAULT 0,
    usage_object_count  bigint NOT NULL DEFAULT 0,
    usage_bytes_today   bigint NOT NULL DEFAULT 0,
    usage_objects_today bigint NOT NULL DEFAULT 0,
    last_reset_at       timestamptz NOT NULL DEFAULT now(),
    resource_version    bigint NOT NULL DEFAULT 1,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    -- A quota scopes to a tenant, a bucket, or the pair — never to neither.
    CONSTRAINT quotas_has_scope
        CHECK (tenant_id IS NOT NULL OR bucket_id IS NOT NULL)
);
CREATE UNIQUE INDEX quotas_tenant_bucket_key
    ON quotas (COALESCE(tenant_id, '00000000-0000-0000-0000-000000000000'::uuid),
               COALESCE(bucket_id, '00000000-0000-0000-0000-000000000000'::uuid));

-- ─── Capabilities and spend ─────────────────────────────────────────────────

CREATE TABLE capability_records (
    id                uuid PRIMARY KEY,
    tenant_id         uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    parent_id         uuid REFERENCES capability_records(id) ON DELETE SET NULL,
    issuer            text NOT NULL,
    principal_kind    text NOT NULL,
    principal_subject text NOT NULL,
    principal_payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    audience          text[] NOT NULL,
    caveats           jsonb NOT NULL,
    generation        bigint NOT NULL DEFAULT 1,
    issued_at         timestamptz NOT NULL DEFAULT now(),
    not_before        timestamptz,
    expires_at        timestamptz NOT NULL
);
CREATE INDEX capability_records_tenant_idx ON capability_records (tenant_id);
CREATE INDEX capability_records_parent_idx ON capability_records (parent_id);
CREATE INDEX capability_records_expiry_idx ON capability_records (expires_at);

CREATE TABLE capability_revocations (
    id         uuid PRIMARY KEY REFERENCES capability_records(id) ON DELETE CASCADE,
    revoked_at timestamptz NOT NULL DEFAULT now(),
    reason     text NOT NULL DEFAULT '',
    actor      text NOT NULL DEFAULT '',
    cascade    boolean NOT NULL DEFAULT false
);

CREATE TABLE capability_usage (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    capability_id uuid NOT NULL UNIQUE REFERENCES capability_records(id) ON DELETE CASCADE,
    request_count bigint NOT NULL DEFAULT 0,
    spent_usd     numeric(14,6) NOT NULL DEFAULT 0,
    unit_code     text NOT NULL DEFAULT 'USD',
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- Per-event ledger and the billing source of truth.
--
-- The references are RESTRICT, NOT CASCADE. Before this baseline both were
-- CASCADE, so deleting a tenant or a capability erased the financial record of
-- what it had spent — a ledger that cannot outlive its subject cannot settle an
-- invoice or answer an audit. Tenant deletion is soft (tenants.deleted_at), so
-- RESTRICT costs nothing in the normal path.
--
-- tenant_slug is captured at charge time so a settled charge reads correctly
-- even after the tenant is renamed.
CREATE TABLE charges (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    tenant_slug   text NOT NULL,
    capability_id uuid NOT NULL REFERENCES capability_records(id) ON DELETE RESTRICT,
    occurred_at   timestamptz NOT NULL DEFAULT now(),
    amount        numeric(14,6) NOT NULL,
    unit_code     text NOT NULL,
    op            text NOT NULL DEFAULT '',
    actor_subject text NOT NULL DEFAULT ''
);
CREATE INDEX charges_tenant_time_idx ON charges (tenant_id, occurred_at DESC);
CREATE INDEX charges_capability_idx ON charges (capability_id);

CREATE TABLE tenant_budgets (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      uuid NOT NULL UNIQUE REFERENCES tenants(id) ON DELETE CASCADE,
    max_budget_usd numeric(14,6),
    spent_usd      numeric(14,6) NOT NULL DEFAULT 0,
    unit_code      text NOT NULL DEFAULT 'USD',
    period_start   timestamptz,
    period_end     timestamptz,
    updated_at     timestamptz NOT NULL DEFAULT now()
);

-- ─── Events ─────────────────────────────────────────────────────────────────

CREATE TABLE event_subscriptions (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    cel_filter       text NOT NULL DEFAULT '',
    sink_kind        event_sink_kind NOT NULL,
    sink_config      jsonb NOT NULL,
    disabled         boolean NOT NULL DEFAULT false,
    resource_version bigint NOT NULL DEFAULT 1,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX event_subscriptions_tenant_idx ON event_subscriptions (tenant_id);

-- Transactional outbox (ADR-0003).
CREATE TABLE event_deliveries (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    subscription_id  uuid NOT NULL REFERENCES event_subscriptions(id) ON DELETE CASCADE,
    event_type       text NOT NULL,
    event_at         timestamptz NOT NULL,
    event_payload    jsonb NOT NULL,
    status           text NOT NULL DEFAULT 'pending',
    attempts         integer NOT NULL DEFAULT 0,
    last_error       text,
    last_status_code integer,
    last_attempt_at  timestamptz,
    next_attempt_at  timestamptz NOT NULL DEFAULT now(),
    delivered_at     timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX event_deliveries_due_idx
    ON event_deliveries (next_attempt_at) WHERE status = 'pending';
CREATE INDEX event_deliveries_depth_idx
    ON event_deliveries (subscription_id, status);

-- Dedup for inbound storage events. `event_id` is the SOURCE's id, so it is
-- text and it is the natural key, not our identity.
CREATE TABLE ingested_events (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id    text NOT NULL,
    source      text NOT NULL,
    type        text NOT NULL,
    subject     text,
    ingested_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source, event_id)
);
CREATE INDEX ingested_events_ingested_at_idx ON ingested_events (ingested_at);

-- ─── Long-running operations ────────────────────────────────────────────────

CREATE TABLE operations (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    type          text NOT NULL,
    state         operation_state NOT NULL DEFAULT 'PENDING',
    metadata      bytea,
    response      bytea,
    error_code    text,
    error_message text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    done_at       timestamptz
);
CREATE INDEX operations_list_keyset_idx
    ON operations (tenant_id, created_at DESC, id);

-- ─── Audit, idempotency, leases ─────────────────────────────────────────────

CREATE TABLE audit_log (
    id              uuid NOT NULL DEFAULT gen_random_uuid(),
    at              timestamptz NOT NULL DEFAULT now(),
    actor_subject   text NOT NULL,
    actor_tenant_id uuid,
    actor_audience  text NOT NULL,
    action          text NOT NULL,
    resource_name   text NOT NULL,
    request_id      text NOT NULL DEFAULT '',
    capability_id   uuid,
    outcome         text NOT NULL DEFAULT '',
    detail          jsonb NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (id, at)
) PARTITION BY RANGE (at);

CREATE TABLE audit_log_default PARTITION OF audit_log DEFAULT;
CREATE INDEX audit_log_at_idx ON audit_log (at DESC);
CREATE INDEX audit_log_actor_tenant_idx ON audit_log (actor_tenant_id, at DESC);

CREATE TABLE idempotency_keys (
    id           uuid NOT NULL DEFAULT gen_random_uuid(),
    tenant_id    uuid NOT NULL,
    method       text NOT NULL,
    key          text NOT NULL,
    response     bytea NOT NULL,
    response_sha bytea NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (id, created_at),
    UNIQUE (tenant_id, method, key, created_at)
) PARTITION BY RANGE (created_at);

CREATE TABLE idempotency_keys_default PARTITION OF idempotency_keys DEFAULT;

CREATE TABLE worker_leases (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL UNIQUE,
    holder_id   uuid NOT NULL,
    holder_meta jsonb NOT NULL DEFAULT '{}'::jsonb,
    generation  bigint NOT NULL DEFAULT 1,
    acquired_at timestamptz NOT NULL DEFAULT now(),
    renewed_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL
);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
-- Consolidated baseline: down is a full teardown. Reprovision, do not migrate.
DROP SCHEMA public CASCADE;
CREATE SCHEMA public;
-- +goose StatementEnd
