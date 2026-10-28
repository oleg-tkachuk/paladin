package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Secret every field below names, and its key.
const (
	fieldSecretName = "field-secret"
	fieldSecretKey  = "value"
	fieldBackend    = "primary"
	fieldClientID   = "paladin-mcp"
)

// secretField is one configuration field a Secret may fill: how a config
// names the Secret, and where the resolved value lands.
type secretField struct {
	path string
	// set points the field at the Secret, turning on whatever gates it.
	set func(c *Config, ref SecretRef)
	// got is the resolved value and whether the reference is gone.
	got func(c *Config) (value string, cleared bool)
}

func ptrRef(ref SecretRef) *SecretRef { return &ref }

// secretFields is every field ResolveConfig fills.
func secretFields() []secretField {
	backend := func(c *Config) StorageBackend { return c.Storage.Backends[fieldBackend] }
	withBackend := func(c *Config, edit func(*StorageBackend)) {
		if c.Storage.Backends == nil {
			c.Storage.Backends = map[string]StorageBackend{}
		}
		b := c.Storage.Backends[fieldBackend]
		edit(&b)
		c.Storage.Backends[fieldBackend] = b
	}
	return []secretField{
		{"postgres.password_secret",
			func(c *Config, r SecretRef) { c.Datastores.Postgres.PasswordSecret = ptrRef(r) },
			func(c *Config) (string, bool) {
				return c.Datastores.Postgres.Password, c.Datastores.Postgres.PasswordSecret == nil
			}},
		{"postgres.migrate_password_secret",
			func(c *Config, r SecretRef) { c.Datastores.Postgres.MigratePasswordSecret = ptrRef(r) },
			func(c *Config) (string, bool) {
				return c.Datastores.Postgres.MigratePassword, c.Datastores.Postgres.MigratePasswordSecret == nil
			}},
		{"postgres.reaper_password_secret",
			func(c *Config, r SecretRef) { c.Datastores.Postgres.ReaperPasswordSecret = ptrRef(r) },
			func(c *Config) (string, bool) {
				return c.Datastores.Postgres.ReaperPassword, c.Datastores.Postgres.ReaperPasswordSecret == nil
			}},
		{"postgres.replica.password_secret",
			func(c *Config, r SecretRef) {
				c.Datastores.Postgres.Replica.Enabled = true
				c.Datastores.Postgres.Replica.PasswordSecret = ptrRef(r)
			},
			func(c *Config) (string, bool) {
				return c.Datastores.Postgres.Replica.Password, c.Datastores.Postgres.Replica.PasswordSecret == nil
			}},
		{"bootstrap.admin.password_secret",
			func(c *Config, r SecretRef) {
				c.Bootstrap.Admin.Enabled = true
				c.Bootstrap.Admin.PasswordSecret = ptrRef(r)
			},
			func(c *Config) (string, bool) {
				return c.Bootstrap.Admin.Password, c.Bootstrap.Admin.PasswordSecret == nil
			}},
		{"auth.signing_key_secret",
			func(c *Config, r SecretRef) { c.Auth.SigningKeySecret = ptrRef(r) },
			func(c *Config) (string, bool) { return c.Auth.SigningKey, c.Auth.SigningKeySecret == nil }},
		{"api_token.hmac_key_secret",
			func(c *Config, r SecretRef) { c.APIToken.HMACKeySecret = ptrRef(r) },
			func(c *Config) (string, bool) { return c.APIToken.HMACKey, c.APIToken.HMACKeySecret == nil }},
		{"capability.dpop_nonce.key_secret",
			func(c *Config, r SecretRef) { c.Capability.DPoPNonce.KeySecret = ptrRef(r) },
			func(c *Config) (string, bool) {
				return c.Capability.DPoPNonce.Key, c.Capability.DPoPNonce.KeySecret == nil
			}},
		{"mcp.dpop_key_secret",
			func(c *Config, r SecretRef) { c.MCP.DPoPKeySecret = ptrRef(r) },
			func(c *Config) (string, bool) { return c.MCP.DPoPKey, c.MCP.DPoPKeySecret == nil }},
		{"runtime.health_snapshot_token_secret",
			func(c *Config, r SecretRef) { c.Runtime.HealthSnapshotTokenSecret = ptrRef(r) },
			func(c *Config) (string, bool) {
				return c.Runtime.HealthSnapshotToken, c.Runtime.HealthSnapshotTokenSecret == nil
			}},
		{"ingest.webhook.shared_secret_ref",
			func(c *Config, r SecretRef) { c.Ingest.Webhook.SharedSecretRef = r },
			func(c *Config) (string, bool) {
				return c.Ingest.Webhook.SharedSecret, c.Ingest.Webhook.SharedSecretRef == SecretRef{}
			}},
		{"ingest.nats.token_ref",
			func(c *Config, r SecretRef) { c.Ingest.NATS.TokenRef = r },
			func(c *Config) (string, bool) { return c.Ingest.NATS.Token, c.Ingest.NATS.TokenRef == SecretRef{} }},
		{"ingest.rabbitmq.url_ref",
			func(c *Config, r SecretRef) { c.Ingest.RabbitMQ.URLRef = r },
			func(c *Config) (string, bool) { return c.Ingest.RabbitMQ.URL, c.Ingest.RabbitMQ.URLRef == SecretRef{} }},
		{"mcp.oauth.exchange.client_secret_ref",
			func(c *Config, r SecretRef) { c.MCP.OAuth.Exchange.ClientSecretRef = ptrRef(r) },
			func(c *Config) (string, bool) {
				return c.MCP.OAuth.Exchange.ClientSecret, c.MCP.OAuth.Exchange.ClientSecretRef == nil
			}},
		{"auth.oauth.seed_clients[" + fieldClientID + "].client_secret_ref",
			func(c *Config, r SecretRef) {
				c.Auth.OAuth.SeedClients = []OAuthSeedClient{{ClientID: fieldClientID, ClientSecretRef: ptrRef(r)}}
			},
			func(c *Config) (string, bool) {
				sc := c.Auth.OAuth.SeedClients[0]
				return sc.ClientSecret, sc.ClientSecretRef == nil
			}},
		{"storage.backends." + fieldBackend + ".auth.access_key_secret",
			func(c *Config, r SecretRef) {
				withBackend(c, func(b *StorageBackend) { b.Auth.AccessKeySecret = ptrRef(r) })
			},
			func(c *Config) (string, bool) {
				return backend(c).Auth.AccessKey, backend(c).Auth.AccessKeySecret == nil
			}},
		{"storage.backends." + fieldBackend + ".auth.secret_key_secret",
			func(c *Config, r SecretRef) {
				withBackend(c, func(b *StorageBackend) { b.Auth.SecretKeySecret = ptrRef(r) })
			},
			func(c *Config) (string, bool) {
				return backend(c).Auth.SecretKey, backend(c).Auth.SecretKeySecret == nil
			}},
		{"storage.backends." + fieldBackend + ".auth.session_token_secret",
			func(c *Config, r SecretRef) {
				withBackend(c, func(b *StorageBackend) { b.Auth.SessionTokenSecret = ptrRef(r) })
			},
			func(c *Config) (string, bool) {
				return backend(c).Auth.SessionToken, backend(c).Auth.SessionTokenSecret == nil
			}},
	}
}

// resolverInCluster is a resolver that passes the pre-flight, over a fake API holding
// the field Secret when present is true.
func resolverInCluster(t *testing.T, present bool) *K8sSecretResolver {
	t.Helper()
	t.Setenv(DefaultK8sServiceHostEnvKey, "10.0.0.1")
	dir := t.TempDir()
	tokenPath, nsPath := filepath.Join(dir, "token"), filepath.Join(dir, "namespace")
	if err := os.WriteFile(tokenPath, []byte("fake-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nsPath, []byte("paladin"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !present {
		return newTestResolver(t, tokenPath, nsPath, secret("paladin", "other", fieldSecretKey, "x"))
	}
	return newTestResolver(t, tokenPath, nsPath, secret("paladin", fieldSecretName, fieldSecretKey, "resolved-"+fieldSecretKey))
}

// Every field a Secret may fill is filled from it and loses its reference;
// one whose Secret is not there fails boot naming the field.
func TestResolveConfigFillsEveryField(t *testing.T) {
	ref := SecretRef{Name: fieldSecretName, Key: fieldSecretKey}
	const want = "resolved-" + fieldSecretKey
	for _, f := range secretFields() {
		t.Run(f.path, func(t *testing.T) {
			cfg := &Config{}
			f.set(cfg, ref)
			if err := resolverInCluster(t, true).ResolveConfig(context.Background(), cfg); err != nil {
				t.Fatalf("ResolveConfig: %v", err)
			}
			if got, cleared := f.got(cfg); got != want || !cleared {
				t.Errorf("value %q, reference cleared %v; want %q and cleared", got, cleared, want)
			}

			missing := &Config{}
			f.set(missing, ref)
			err := resolverInCluster(t, false).ResolveConfig(context.Background(), missing)
			if err == nil || !strings.HasPrefix(err.Error(), f.path+":") {
				t.Errorf("Secret absent: err = %v, want one naming %s", err, f.path)
			}
		})
	}
}

// A replica or bootstrap admin that is off reads no Secret, so a stale
// reference left behind cannot stop boot.
func TestResolveConfigSkipsWhatIsOff(t *testing.T) {
	absent := &SecretRef{Name: "absent", Key: fieldSecretKey}
	cfg := &Config{}
	cfg.Datastores.Postgres.Replica.PasswordSecret = absent
	cfg.Bootstrap.Admin.PasswordSecret = absent
	if err := resolverInCluster(t, false).ResolveConfig(context.Background(), cfg); err != nil {
		t.Fatalf("ResolveConfig with the replica and bootstrap off: %v", err)
	}
	if cfg.Datastores.Postgres.Replica.PasswordSecret == nil || cfg.Bootstrap.Admin.PasswordSecret == nil {
		t.Error("a reference of a feature that is off was resolved")
	}
}
