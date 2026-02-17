-- +goose NO TRANSACTION
-- +goose Up

-- Role creation and grant moved to IAC or handled by table owner (app_user).
-- Deleting this migration's logic as it requires superuser/CREATEROLE permissions.

-- +goose Down
