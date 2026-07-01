-- +goose Up
-- +goose StatementBegin
-- 048_storage_backend_health.sql
--
-- Observed health state for storage backends (BACKLOG: "Backend states beyond
-- enable/disable" → maintenance/error auto-states). Distinct from the operator
-- gates `enabled` (037) and `read_only` (047): health is DERIVED — TestBackend
-- writes it — and ADVISORY. It is surfaced in the admin UI but does NOT gate
-- operations, because a transient probe failure must never silently take a
-- backend offline; the operator decides whether to disable/drain in response.
--
-- It lives in a SEPARATE 1:1 table rather than columns on storage_backends so
-- that recording a probe result does NOT fire the storage_backends
-- bump_resource_version trigger — a health write must not churn the backend's
-- resource_version (invalidating operators' in-flight OCC tokens) or its
-- updated_at, and TestBackend stays read-only w.r.t. the config row.
--
--   status: unknown → never probed · ok → last probe succeeded ·
--           error → last probe failed (message carries why).
CREATE TABLE storage_backend_health (
    backend_id TEXT PRIMARY KEY
        REFERENCES storage_backends (id) ON DELETE CASCADE,
    status     TEXT NOT NULL DEFAULT 'unknown'
        CHECK (status IN ('unknown', 'ok', 'error')),
    message    TEXT        NOT NULL DEFAULT '',
    checked_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS storage_backend_health;
-- +goose StatementEnd
