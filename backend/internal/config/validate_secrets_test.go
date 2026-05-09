package config

import (
	"strings"
	"testing"
)

// Each test below builds the smallest Config that passes the
// non-secret validation gates (DSN + app.name + storage default
// backend) and then introduces ONE both-set conflict at the field
// under test. Validate() must reject with a message that names the
// field — operators rely on the path in the error to pinpoint the
// drift.

func minimalValidConfig() Config {
	return Config{
		App: App{Name: "paladin"},
		Datastores: Datastores{
			Postgres: Postgres{DSN: "postgres://x@y/z"},
		},
		Storage: Storage{
			DefaultBackend: "primary",
			Backends: map[string]StorageBackend{
				"primary": {
					Auth: StorageBackendAuth{Mode: AuthModeStaticKeys, AccessKey: "k", SecretKey: "s"},
				},
			},
		},
	}
}

func mustReject(t *testing.T, c Config, wantSubstr string) {
	t.Helper()
	err := c.Validate()
	if err == nil {
		t.Fatalf("Validate(): want error containing %q, got nil", wantSubstr)
	}
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Errorf("Validate(): error = %v, want substring %q", err, wantSubstr)
	}
}

func TestValidate_SecretMutex_PostgresPassword(t *testing.T) {
	c := minimalValidConfig()
	c.Datastores.Postgres.Password = "inline"
	c.Datastores.Postgres.PasswordSecret = &SecretRef{Name: "x", Key: "k"}
	mustReject(t, c, "postgres: cannot specify both password and password_secret")
}

func TestValidate_SecretMutex_PostgresMigratePassword(t *testing.T) {
	c := minimalValidConfig()
	c.Datastores.Postgres.MigratePassword = "inline"
	c.Datastores.Postgres.MigratePasswordSecret = &SecretRef{Name: "x", Key: "k"}
	mustReject(t, c, "migrate_password and migrate_password_secret")
}

func TestValidate_SecretMutex_AuthSigningKey(t *testing.T) {
	c := minimalValidConfig()
	c.Auth.SigningKey = "32-bytes-of-secret-data-here-please"
	c.Auth.SigningKeySecret = &SecretRef{Name: "x", Key: "k"}
	mustReject(t, c, "auth: cannot specify both signing_key and signing_key_secret")
}

func TestValidate_SecretMutex_BootstrapAdminPassword(t *testing.T) {
	c := minimalValidConfig()
	c.Bootstrap.Admin.Enabled = true
	c.Bootstrap.Admin.Password = "inline"
	c.Bootstrap.Admin.PasswordSecret = &SecretRef{Name: "x", Key: "k"}
	mustReject(t, c, "bootstrap.admin: cannot specify both password and password_secret")
}

// When bootstrap.admin.enabled=false the mutex is moot — operators
// often leave inline AND password_secret in YAML for a follow-up
// scenario. Don't reject the disabled case.
func TestValidate_SecretMutex_BootstrapAdminDisabledTolerates(t *testing.T) {
	c := minimalValidConfig()
	c.Bootstrap.Admin.Enabled = false
	c.Bootstrap.Admin.Password = "inline"
	c.Bootstrap.Admin.PasswordSecret = &SecretRef{Name: "x", Key: "k"}
	if err := c.Validate(); err != nil {
		t.Errorf("Validate() with bootstrap disabled: want nil, got %v", err)
	}
}

func TestValidate_SecretMutex_StorageSessionToken(t *testing.T) {
	c := minimalValidConfig()
	b := c.Storage.Backends["primary"]
	b.Auth.SessionToken = "inline"
	b.Auth.SessionTokenSecret = &SecretRef{Name: "x", Key: "k"}
	c.Storage.Backends["primary"] = b
	mustReject(t, c, "session_token and session_token_secret")
}
