-- +goose Up
-- +goose StatementBegin
-- 038_storage_backend_credential_rotation.sql
--
-- Adds the grace-window state for RotateCredentials. When an operator
-- rotates a backend's credentials_secret_ref, the OLD ref is preserved in
-- previous_credentials_secret_ref with previous_credentials_valid_until set
-- to now() + grace_period. This lets operator tooling (and a future
-- paladin.backend.credentials_rotated consumer) know that a presign / in-flight
-- request signed with the old credentials is expected to keep working until
-- the window elapses, instead of the rotation looking instantaneous.
--
-- Both columns are nullable with no default — a metadata-only ALTER (no
-- table rewrite, instant on a populated table; see migrations/CONVENTIONS.md).
-- NULL previous_* means "no rotation has happened" / "grace window cleared".
ALTER TABLE storage_backends
    ADD COLUMN previous_credentials_secret_ref  TEXT,
    ADD COLUMN previous_credentials_valid_until TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE storage_backends
    DROP COLUMN IF EXISTS previous_credentials_valid_until,
    DROP COLUMN IF EXISTS previous_credentials_secret_ref;
-- +goose StatementEnd
