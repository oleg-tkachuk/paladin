-- +goose Up
-- +goose StatementBegin

-- The second dark palette is renamed "violet" → "ember": its accent is orange.
-- A saved "violet" carries over. The accepted set mirrors apiutil.Themes.
ALTER TABLE user_settings DROP CONSTRAINT user_settings_theme_check;
UPDATE user_settings SET theme = 'ember' WHERE theme = 'violet';
ALTER TABLE user_settings ADD CONSTRAINT user_settings_theme_check
    CHECK (theme IN ('system', 'light', 'dark', 'ember')) NOT VALID;
ALTER TABLE user_settings VALIDATE CONSTRAINT user_settings_theme_check;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE user_settings DROP CONSTRAINT user_settings_theme_check;
UPDATE user_settings SET theme = 'violet' WHERE theme = 'ember';
ALTER TABLE user_settings ADD CONSTRAINT user_settings_theme_check
    CHECK (theme IN ('system', 'light', 'dark', 'violet')) NOT VALID;
ALTER TABLE user_settings VALIDATE CONSTRAINT user_settings_theme_check;
-- +goose StatementEnd
