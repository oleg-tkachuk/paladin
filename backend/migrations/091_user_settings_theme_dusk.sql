-- +goose Up
-- +goose StatementBegin

-- The plum palette is renamed "ember" → "dusk": its ground was lifted and its
-- sidebar turned aubergine, and the old name no longer fits. A saved "ember"
-- carries over. The accepted set mirrors apiutil.Themes.
ALTER TABLE user_settings DROP CONSTRAINT user_settings_theme_check;
UPDATE user_settings SET theme = 'dusk' WHERE theme = 'ember';
ALTER TABLE user_settings ADD CONSTRAINT user_settings_theme_check
    CHECK (theme IN ('system', 'light', 'dark', 'dusk', 'navy', 'graphite', 'bordeaux')) NOT VALID;
ALTER TABLE user_settings VALIDATE CONSTRAINT user_settings_theme_check;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE user_settings DROP CONSTRAINT user_settings_theme_check;
UPDATE user_settings SET theme = 'ember' WHERE theme = 'dusk';
ALTER TABLE user_settings ADD CONSTRAINT user_settings_theme_check
    CHECK (theme IN ('system', 'light', 'dark', 'ember', 'navy', 'graphite', 'bordeaux')) NOT VALID;
ALTER TABLE user_settings VALIDATE CONSTRAINT user_settings_theme_check;
-- +goose StatementEnd
