package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// weakSecretConfig is minimalValidConfig() promoted to a non-disposable
// environment, which is the only state in which the gate does anything.
func weakSecretConfig() Config {
	c := minimalValidConfig()
	c.App.Env = "prod"
	return c
}

func TestWeakSecretGateIsSilentInDisposableEnvs(t *testing.T) {
	// The committed defaults MUST keep working on a laptop — a gate that
	// breaks `task stack:up` gets disabled, not fixed.
	for env := range disposableEnvs {
		c := minimalValidConfig()
		c.App.Env = env
		c.Auth.SigningKey = "dev-secret-change-me-32-bytes-min"
		if err := c.Validate(); err != nil {
			t.Errorf("app.env=%q: committed default must be accepted, got %v", env, err)
		}
	}
}

func TestWeakSecretGateRejectsCommittedDefaults(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		field  string
	}{
		{
			name:   "auth signing key",
			mutate: func(c *Config) { c.Auth.SigningKey = "dev-secret-change-me-32-bytes-min" },
			field:  "auth.signing_key",
		},
		{
			name:   "api token hmac key",
			mutate: func(c *Config) { c.APIToken.HMACKey = "changeme-please" },
			field:  "api_token.hmac_key",
		},
		{
			name: "bootstrap admin password",
			mutate: func(c *Config) {
				c.Bootstrap.Admin.Enabled = true
				c.Bootstrap.Admin.Password = "admin-dev-password-change-me"
			},
			field: "bootstrap.admin.password",
		},
		{
			name:   "postgres password",
			mutate: func(c *Config) { c.Datastores.Postgres.Password = "paladin" },
			field:  "datastores.postgres.password",
		},
		{
			name:   "health snapshot token placeholder",
			mutate: func(c *Config) { c.Runtime.HealthSnapshotToken = "<prod-health-token-from-sealed-secret>" },
			field:  "runtime.health_snapshot_token",
		},
		{
			// Every chart values file spells "fill this in" with angle
			// brackets, and the texts inside them differ per file — the
			// shape is what is recognised, not the wording.
			name:   "angle-bracket placeholder with unseen wording",
			mutate: func(c *Config) { c.Auth.SigningKey = "<dev-cluster-signing-key>" },
			field:  "auth.signing_key",
		},
		{
			name: "storage backend secret key",
			mutate: func(c *Config) {
				b := c.Storage.Backends["primary"]
				b.Auth.SecretKey = "paladin-e2e-secret-key"
				c.Storage.Backends["primary"] = b
			},
			field: "storage.backends.primary.auth.secret_key",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := weakSecretConfig()
			tc.mutate(&c)

			err := c.Validate()
			if err == nil {
				t.Fatalf("expected %s to be rejected in app.env=prod", tc.field)
			}
			// Operators pinpoint the offending key from the path in the
			// message — an error that doesn't name the field is a support
			// ticket, so assert on it rather than on "an error happened".
			if !strings.Contains(err.Error(), tc.field) {
				t.Errorf("error must name %s, got: %v", tc.field, err)
			}
		})
	}
}

func TestWeakSecretGateReportsEveryOffenderAtOnce(t *testing.T) {
	c := weakSecretConfig()
	c.Auth.SigningKey = "dev-secret-change-me-32-bytes-min"
	c.APIToken.HMACKey = "replace-me"

	err := c.Validate()
	if err == nil {
		t.Fatal("expected rejection")
	}
	for _, want := range []string{"auth.signing_key", "api_token.hmac_key"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("all offenders must be listed in one pass; %s missing from: %v", want, err)
		}
	}
}

func TestWeakSecretGateAcceptsRealSecrets(t *testing.T) {
	// A real secret must pass regardless of length or shape — this is not
	// an entropy check, and a gate with false positives gets worked around.
	for _, secret := range []string{
		"Qb8mR2xK9wL4vN7pT1cF5hJ3sD6gY0zA",
		"0123456789abcdef0123456789abcdef",
		"correct-horse-battery-staple-2026",
	} {
		c := weakSecretConfig()
		c.Auth.SigningKey = secret
		if err := c.Validate(); err != nil {
			t.Errorf("secret %q must be accepted, got %v", secret, err)
		}
	}
}

func TestWeakSecretGateIgnoresEmptyValues(t *testing.T) {
	// Unset is a different failure with a different owner (the required-field
	// checks). Reporting it here would give one mistake two error messages.
	c := weakSecretConfig()
	c.Auth.SigningKey = ""
	if err := c.Validate(); err != nil {
		t.Errorf("empty value must not trip the weak-secret gate, got %v", err)
	}
}

// TestWeakSecretsCoverCommittedDefaults is the contract that keeps
// weakSecretValues honest: every credential literal this repository ships in
// a config file must be recognised by the gate. Without it the deny-list rots
// the moment someone adds a new default, and the gate silently stops
// protecting the thing it was written for.
func TestWeakSecretsCoverCommittedDefaults(t *testing.T) {
	// Paths relative to backend/internal/config.
	configs := []string{
		"../../configs/config.yaml",
		"../../configs/local.yaml",
		"../../configs/compose.yaml",
	}

	// Keys whose committed values are credentials. Matched on the YAML key
	// name, so nesting doesn't have to be modelled.
	credentialKeys := []string{
		"signing_key:",
		"hmac_key:",
		"password:",
		"secret_key:",
		"shared_secret:",
		"health_snapshot_token:",
	}

	for _, path := range configs {
		body, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for i, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				continue
			}
			var matched bool
			for _, key := range credentialKeys {
				if strings.HasPrefix(trimmed, key) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}

			value := extractYAMLScalar(trimmed)
			// Empty, env-var indirection and SecretRef siblings carry no
			// literal to classify.
			if value == "" || strings.HasPrefix(value, "${") {
				continue
			}
			if !isWeakSecret(value) {
				t.Errorf(
					"%s:%d ships credential %q that the weak-secret gate does not "+
						"recognise — add it to weakSecretValues (or stop committing it)",
					path, i+1, value)
			}
		}
	}
}

// extractYAMLScalar pulls the scalar value out of a `key: value  # comment`
// line. Good enough for the flat credential lines the coverage test scans;
// deliberately not a YAML parser.
func extractYAMLScalar(line string) string {
	_, after, found := strings.Cut(line, ":")
	if !found {
		return ""
	}
	value := strings.TrimSpace(after)
	if idx := strings.Index(value, " #"); idx >= 0 {
		value = strings.TrimSpace(value[:idx])
	}
	return strings.Trim(value, `"'`)
}
