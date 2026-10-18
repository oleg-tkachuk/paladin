package config

import (
	"github.com/oleg-tkachuk/limes"

	"strings"
	"testing"
	"time"
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
		Limits:     validLimits(),
		Admin:      Admin{Tenants: AdminTenants{TrashRetention: 720 * time.Hour}},
		Capability: Capability{Currency: limes.AbstractUnitCode},
		Storage: Storage{
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

func TestValidate_SecretMutex_PostgresReaperPassword(t *testing.T) {
	c := minimalValidConfig()
	c.Datastores.Postgres.ReaperPassword = "inline"
	c.Datastores.Postgres.ReaperPasswordSecret = &SecretRef{Name: "x", Key: "k"}
	mustReject(t, c, "reaper_password and reaper_password_secret")
}

func TestValidate_SecretMutex_AuthSigningKey(t *testing.T) {
	c := minimalValidConfig()
	c.Auth.SigningKey = "32-bytes-of-secret-data-here-please"
	c.Auth.SigningKeySecret = &SecretRef{Name: "x", Key: "k"}
	mustReject(t, c, "auth: cannot specify both signing_key and signing_key_secret")
}

func TestValidate_SecretMutex_HealthSnapshotToken(t *testing.T) {
	c := minimalValidConfig()
	c.Runtime.HealthSnapshotToken = "inline"
	c.Runtime.HealthSnapshotTokenSecret = &SecretRef{Name: "x", Key: "k"}
	mustReject(t, c, "runtime: cannot specify both health_snapshot_token and health_snapshot_token_secret")
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

// Ingest webhook auth: an unauthenticated receiver is rejected in
// staging/prod (forged PROMOTE events run with BYPASSRLS) but tolerated
// in dev where the publisher signs with the empty secret.
func TestValidate_IngestWebhook_RequiresSecretInProd(t *testing.T) {
	c := minimalValidConfig()
	c.App.Env = "prod"
	c.Ingest.Enabled = true
	c.Ingest.Driver = "webhook"
	mustReject(t, c, "ingest.webhook: shared_secret or shared_secret_ref is required")
}

func TestValidate_IngestWebhook_SecretRefSatisfiesProd(t *testing.T) {
	c := minimalValidConfig()
	c.App.Env = "prod"
	c.Ingest.Enabled = true
	c.Ingest.Driver = "webhook"
	c.Ingest.Webhook.SharedSecretRef = SecretRef{Name: "ingest-hmac", Key: "secret"}
	if err := c.Validate(); err != nil {
		t.Errorf("Validate() with shared_secret_ref set: want nil, got %v", err)
	}
}

func TestValidate_IngestWebhook_DevToleratesNoSecret(t *testing.T) {
	c := minimalValidConfig()
	c.App.Env = "local"
	c.Ingest.Enabled = true
	c.Ingest.Driver = "webhook"
	if err := c.Validate(); err != nil {
		t.Errorf("Validate() in local env without secret: want nil, got %v", err)
	}
}

func TestValidate_OAuthSeedClientAuthentication(t *testing.T) {
	ref := &SecretRef{Name: "x", Key: "k"}
	for _, tc := range []struct {
		name   string
		client OAuthSeedClient
		want   string // "" accepts
	}{
		{"public, no secret", OAuthSeedClient{ClientID: "c", Public: true}, ""},
		{"confidential, inline secret", OAuthSeedClient{ClientID: "c", ClientSecret: "s"}, ""},
		{"confidential, secret ref", OAuthSeedClient{ClientID: "c", ClientSecretRef: ref}, ""},
		{"confidential exchanger", OAuthSeedClient{ClientID: "c", ClientSecretRef: ref, TokenExchange: true}, ""},
		{"both secrets", OAuthSeedClient{ClientID: "c", ClientSecret: "s", ClientSecretRef: ref}, "cannot specify both client_secret and client_secret_ref"},
		{"public with a secret", OAuthSeedClient{ClientID: "c", Public: true, ClientSecret: "s"}, "a public client takes no client_secret"},
		{"confidential without a secret", OAuthSeedClient{ClientID: "c"}, "a confidential client needs client_secret or client_secret_ref"},
		{"public exchanger", OAuthSeedClient{ClientID: "c", Public: true, TokenExchange: true}, "token_exchange needs a confidential client"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := minimalValidConfig()
			c.Auth.OAuth.SeedClients = []OAuthSeedClient{tc.client}
			if tc.want == "" {
				if err := c.Validate(); err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			mustReject(t, c, "auth.oauth.seed_clients[c]: "+tc.want)
		})
	}
}

func TestValidate_MCPExchange(t *testing.T) {
	ref := &SecretRef{Name: "x", Key: "k"}
	on := func(ex MCPOAuthExchange) MCPOAuth { return MCPOAuth{Enabled: true, Exchange: ex} }
	for _, tc := range []struct {
		name  string
		oauth MCPOAuth
		want  string // "" accepts
	}{
		{"off", MCPOAuth{}, ""},
		{"inline secret", on(MCPOAuthExchange{ClientID: "mcp", ClientSecret: "s"}), ""},
		{"secret ref", on(MCPOAuthExchange{ClientID: "mcp", ClientSecretRef: ref}), ""},
		{"both secrets", on(MCPOAuthExchange{ClientID: "mcp", ClientSecret: "s", ClientSecretRef: ref}), "cannot specify both client_secret and client_secret_ref"},
		{"no secret", on(MCPOAuthExchange{ClientID: "mcp"}), "client_id needs client_secret or client_secret_ref"},
		{"a secret with no client", on(MCPOAuthExchange{ClientSecret: "s"}), "a client secret needs a client_id"},
		{"oauth off at the edge", MCPOAuth{Exchange: MCPOAuthExchange{ClientID: "mcp", ClientSecret: "s"}}, "needs mcp.oauth.enabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := minimalValidConfig()
			c.MCP.OAuth = tc.oauth
			if tc.want == "" {
				if err := c.Validate(); err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				if got, want := c.MCP.OAuth.ExchangeEnabled(), tc.oauth.Exchange.ClientID != ""; got != want {
					t.Errorf("ExchangeEnabled() = %v, want %v", got, want)
				}
				return
			}
			mustReject(t, c, "mcp.oauth.exchange: "+tc.want)
		})
	}
}

// The ingest credentials take a value or a reference, never both: until the
// references were resolved, setting one beside a value silently used the value.
func TestValidate_SecretMutex_Ingest(t *testing.T) {
	ref := SecretRef{Name: "x", Key: "k"}
	cases := []struct {
		name string
		set  func(*Config)
		want string
	}{
		{"webhook", func(c *Config) {
			c.Ingest.Webhook.SharedSecret, c.Ingest.Webhook.SharedSecretRef = "inline", ref
		}, "ingest.webhook: cannot specify both shared_secret and shared_secret_ref"},
		{"nats", func(c *Config) {
			c.Ingest.NATS.Token, c.Ingest.NATS.TokenRef = "inline", ref
		}, "ingest.nats: cannot specify both token and token_ref"},
		{"rabbitmq", func(c *Config) {
			c.Ingest.RabbitMQ.URL, c.Ingest.RabbitMQ.URLRef = "amqp://u:p@h/", ref
		}, "ingest.rabbitmq: cannot specify both url and url_ref"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := minimalValidConfig()
			tc.set(&c)
			mustReject(t, c, tc.want)
		})
	}
	// Either one alone is accepted.
	for _, set := range []func(*Config){
		func(c *Config) { c.Ingest.NATS.TokenRef = ref },
		func(c *Config) { c.Ingest.NATS.Token = "inline" },
		func(c *Config) { c.Ingest.RabbitMQ.URLRef = ref },
		func(c *Config) { c.Ingest.RabbitMQ.URL = "amqp://u:p@h/" },
	} {
		c := minimalValidConfig()
		set(&c)
		if err := c.Validate(); err != nil {
			t.Errorf("Validate() = %v, want one of value or reference accepted", err)
		}
	}
}
