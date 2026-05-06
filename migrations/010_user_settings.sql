-- +goose Up
-- +goose StatementBegin

-- Per-user settings — what the web UI persists on behalf of an end user.
--
-- Two-tier model:
--   1. Explicit columns for settings the platform reasons about server-side
--      (timezone for log/event rendering, locale for i18n, theme for the
--      "send-me-an-email" flows). These are the contract surface and have
--      validation/check constraints.
--   2. `preferences JSONB` for free-form, UI-only state (collapsed sidebar,
--      preferred page size, dismissed banners, etc.). Treated as opaque on
--      the server — the web client owns the schema.
--
-- 1:1 with users (one row per user). Created lazily on first write — a user
-- with no row falls back to defaults at the read path.
--
-- Tenant denormalization: `tenant_id` is duplicated from `users.tenant_id`
-- so per-tenant listings (admin "show all timezones in our org") do not need
-- a join. The CHECK trigger keeps the two in sync.

CREATE TABLE user_settings (
    user_id          UUID PRIMARY KEY REFERENCES users(user_id) ON DELETE CASCADE,
    tenant_id        UUID NOT NULL REFERENCES tenants(tenant_id) ON DELETE RESTRICT,

    -- IANA tz database name (e.g. "Europe/Kyiv"). UTC fallback keeps the
    -- column NOT NULL — clients that haven't configured a timezone yet
    -- still get deterministic server-side rendering.
    timezone         TEXT NOT NULL DEFAULT 'UTC',
    -- BCP-47 language tag (e.g. "uk-UA", "en"). Loose CHECK only — full
    -- validation lives in the application layer (apiutil.ValidateLocale).
    locale           TEXT NOT NULL DEFAULT 'en-US',
    -- "light" | "dark" | "system". Open-ended for future themes; CHECK
    -- bounds the universe to avoid silent typos persisting forever.
    theme            TEXT NOT NULL DEFAULT 'system',

    -- UI-only state. Server treats as opaque; the web client owns the
    -- shape. Capped at 16 KiB on write (handler-level) so a runaway tab
    -- can't blow up the row.
    preferences      JSONB NOT NULL DEFAULT '{}'::jsonb,

    resource_version BIGINT NOT NULL DEFAULT 1,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT user_settings_timezone_format
        CHECK (length(timezone) BETWEEN 1 AND 64
               AND timezone ~ '^[A-Za-z][A-Za-z0-9_+\-/]*$'),
    CONSTRAINT user_settings_locale_format
        CHECK (length(locale) BETWEEN 2 AND 35
               AND locale ~ '^[A-Za-z]{2,3}([-_][A-Za-z0-9]{2,8})*$'),
    CONSTRAINT user_settings_theme_enum
        CHECK (theme IN ('light', 'dark', 'system'))
);

-- Tenant-scoped lookups (admin: list-all-timezones, GDPR export, etc.).
CREATE INDEX idx_user_settings_tenant ON user_settings(tenant_id);

-- Auto-bump resource_version on UPDATE so OCC works without app help.
DROP TRIGGER IF EXISTS trg_user_settings_bump_rv ON user_settings;
CREATE TRIGGER trg_user_settings_bump_rv
    BEFORE UPDATE ON user_settings
    FOR EACH ROW EXECUTE FUNCTION bump_resource_version();

-- Tenant consistency: tenant_id must match users.tenant_id. Enforced via a
-- trigger because PG doesn't support FOREIGN KEY referencing a derived
-- tuple. Cross-tenant inserts would otherwise silently corrupt isolation.
CREATE OR REPLACE FUNCTION enforce_user_settings_tenant() RETURNS trigger
    LANGUAGE plpgsql
    SECURITY INVOKER
    SET search_path = pg_catalog, public
    AS $$
DECLARE
    expected_tenant UUID;
BEGIN
    SELECT tenant_id INTO expected_tenant FROM users WHERE user_id = NEW.user_id;
    IF expected_tenant IS NULL THEN
        RAISE EXCEPTION 'user_settings: user_id % does not exist', NEW.user_id;
    END IF;
    IF NEW.tenant_id <> expected_tenant THEN
        RAISE EXCEPTION 'user_settings: tenant_id mismatch (got %, want %)',
            NEW.tenant_id, expected_tenant;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_user_settings_enforce_tenant ON user_settings;
CREATE TRIGGER trg_user_settings_enforce_tenant
    BEFORE INSERT OR UPDATE OF tenant_id, user_id ON user_settings
    FOR EACH ROW EXECUTE FUNCTION enforce_user_settings_tenant();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS trg_user_settings_enforce_tenant ON user_settings;
DROP FUNCTION IF EXISTS enforce_user_settings_tenant();
DROP TRIGGER IF EXISTS trg_user_settings_bump_rv ON user_settings;
DROP TABLE IF EXISTS user_settings;

-- +goose StatementEnd
