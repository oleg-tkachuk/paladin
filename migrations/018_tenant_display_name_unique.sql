-- +goose Up
-- +goose StatementBegin
ALTER TABLE tenants ADD CONSTRAINT uq_tenants_display_name UNIQUE (display_name);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE tenants DROP CONSTRAINT IF EXISTS uq_tenants_display_name;
-- +goose StatementEnd
