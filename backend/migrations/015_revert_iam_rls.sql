-- +goose Up
-- +goose StatementBegin

-- Reverts 014. It broke login on the cluster within minutes of deploying:
--
--   persist refresh: ERROR: new row violates row-level security policy
--   for table "refresh_tokens" (SQLSTATE 42501)
--
-- The mistake was scoping the pre-auth exemption to SELECT. Login has no
-- session tenant yet — that is the whole point — so `WITH CHECK (tenant_id =
-- paladin_session_tenant_id())` compares against NULL and refuses the INSERT.
-- The same applies to every other pre-session write on these tables:
-- TouchUserLogin updates `users` at login, and rotation supersedes and
-- inserts `refresh_tokens` rows before any session exists. A SELECT-only
-- exemption cannot admit any of them.
--
-- 014 was verified against the compose stack, which does not exercise this at
-- all: it connects as the owning role, so FORCE RLS is the only thing that
-- would apply and the app never runs as `paladin_app` there. The cluster does.
-- A green 108-test e2e run therefore said nothing about RLS, which is the
-- lesson worth keeping.
--
-- All four are reverted rather than only the two that broke, because the pair
-- that survived did so by accident of test coverage, not by having been shown
-- correct. The redo needs the whole pre-auth write surface mapped first —
-- INSERT, UPDATE and DELETE, not just reads.

DROP POLICY IF EXISTS users_preauth_read ON users;
DROP POLICY IF EXISTS tenant_isolation ON users;
ALTER TABLE users NO FORCE ROW LEVEL SECURITY;
ALTER TABLE users DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS refresh_tokens_preauth_read ON refresh_tokens;
DROP POLICY IF EXISTS tenant_isolation ON refresh_tokens;
ALTER TABLE refresh_tokens NO FORCE ROW LEVEL SECURITY;
ALTER TABLE refresh_tokens DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation ON user_settings;
ALTER TABLE user_settings NO FORCE ROW LEVEL SECURITY;
ALTER TABLE user_settings DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation ON tenant_default_bindings;
ALTER TABLE tenant_default_bindings NO FORCE ROW LEVEL SECURITY;
ALTER TABLE tenant_default_bindings DISABLE ROW LEVEL SECURITY;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Intentionally empty: rolling back a revert would restore a migration known
-- to break login. Re-apply the corrected policies in a new migration instead.
-- +goose StatementEnd
