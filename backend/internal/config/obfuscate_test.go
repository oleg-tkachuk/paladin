package config

import "testing"

// TestObfuscated_RedactsAllCredentials pins that Obfuscated() — used for the
// "config loaded" boot log — masks every resolved secret, not just the runtime
// DSN password. The resolver fills these from *_password_secret at boot, so a
// gap here leaks the cleartext credential into pod logs.
func TestObfuscated_RedactsAllCredentials(t *testing.T) {
	c := Config{}
	c.Datastores.Postgres.Password = "runtime-secret"
	c.Datastores.Postgres.MigratePassword = "migrate-secret"
	c.Datastores.Postgres.ReaperPassword = "reaper-secret"
	c.Auth.SigningKey = "signing-secret"
	c.Bootstrap.Admin.Password = "admin-secret"

	o := c.Obfuscated()

	checks := map[string]string{
		"postgres.password":         o.Datastores.Postgres.Password,
		"postgres.migrate_password": o.Datastores.Postgres.MigratePassword,
		"postgres.reaper_password":  o.Datastores.Postgres.ReaperPassword,
		"auth.signing_key":          o.Auth.SigningKey,
		"bootstrap.admin.password":  o.Bootstrap.Admin.Password,
	}
	for field, got := range checks {
		if got != Redacted {
			t.Errorf("%s = %q, want %q (leaked into the config log)", field, got, Redacted)
		}
	}

	// Obfuscated() must not mutate the source config.
	if c.Datastores.Postgres.ReaperPassword != "reaper-secret" {
		t.Errorf("Obfuscated() mutated the source: reaper_password = %q", c.Datastores.Postgres.ReaperPassword)
	}
}
