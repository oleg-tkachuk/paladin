-- +goose Up
-- +goose StatementBegin

-- The console gains a second dark palette, "violet". The accepted set mirrors
-- apiutil.Themes. NOT VALID then VALIDATE, so the scan of existing rows runs
-- under SHARE UPDATE EXCLUSIVE rather than ACCESS EXCLUSIVE (CONVENTIONS.md).
ALTER TABLE user_settings DROP CONSTRAINT user_settings_theme_check;
ALTER TABLE user_settings ADD CONSTRAINT user_settings_theme_check
    CHECK (theme IN ('system', 'light', 'dark', 'violet')) NOT VALID;
ALTER TABLE user_settings VALIDATE CONSTRAINT user_settings_theme_check;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE user_settings SET theme = 'dark' WHERE theme = 'violet';
ALTER TABLE user_settings DROP CONSTRAINT user_settings_theme_check;
ALTER TABLE user_settings ADD CONSTRAINT user_settings_theme_check
    CHECK (theme IN ('system', 'light', 'dark'));
-- +goose StatementEnd
