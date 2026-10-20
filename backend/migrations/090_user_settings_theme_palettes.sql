-- +goose Up
-- +goose StatementBegin

-- Three more dark palettes: navy, graphite and bordeaux. The accepted set
-- mirrors apiutil.Themes.
ALTER TABLE user_settings DROP CONSTRAINT user_settings_theme_check;
ALTER TABLE user_settings ADD CONSTRAINT user_settings_theme_check
    CHECK (theme IN ('system', 'light', 'dark', 'ember', 'navy', 'graphite', 'bordeaux')) NOT VALID;
ALTER TABLE user_settings VALIDATE CONSTRAINT user_settings_theme_check;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE user_settings DROP CONSTRAINT user_settings_theme_check;
-- A saved palette the older set lacks falls back to the default dark one.
UPDATE user_settings SET theme = 'dark' WHERE theme IN ('navy', 'graphite', 'bordeaux');
ALTER TABLE user_settings ADD CONSTRAINT user_settings_theme_check
    CHECK (theme IN ('system', 'light', 'dark', 'ember')) NOT VALID;
ALTER TABLE user_settings VALIDATE CONSTRAINT user_settings_theme_check;
-- +goose StatementEnd
