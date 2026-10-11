package config

import (
	"testing"
)

// Load reads PALADIN_-prefixed variables over the files, resolving each
// against the schema, and ignores everything without the prefix.
func TestLoadReadsPrefixedEnv(t *testing.T) {
	const (
		fromEnvName = "from-env"
		fromEnvKey  = "from-env-key"
	)
	t.Setenv(EnvPrefix+"APP_NAME", fromEnvName)
	// A leaf whose own name holds an underscore: access_key, not access.key.
	t.Setenv(EnvPrefix+"STORAGE_BACKENDS_PRIMARY_AUTH_ACCESS_KEY", fromEnvKey)
	t.Setenv("APP_ENV", "prod")

	cfg := loadWith(t, "")
	if cfg.App.Name != fromEnvName {
		t.Errorf("app.name = %q, want %q from the environment", cfg.App.Name, fromEnvName)
	}
	if got := cfg.Storage.Backends["primary"].Auth.AccessKey; got != fromEnvKey {
		t.Errorf("storage.backends.primary.auth.access_key = %q, want %q from the environment", got, fromEnvKey)
	}
	if cfg.App.Env != "local" {
		t.Errorf("app.env = %q, want the file's \"local\": APP_ENV has no prefix", cfg.App.Env)
	}
}
