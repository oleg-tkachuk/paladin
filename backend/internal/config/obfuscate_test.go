package config

import "testing"

// TestObfuscated_RedactsAllCredentials pins that Obfuscated() — used for the
// "config loaded" boot log — masks every resolved secret, not just the runtime
// DSN password. The resolver fills these from *_secret refs at boot, so a gap
// here leaks the cleartext credential into pod logs.
//
// api_token.hmac_key was such a gap: a deployment that set a dedicated pepper
// instead of deriving one from the signing key printed that pepper at every
// boot, which is exactly what makes a stolen api_tokens dump forgeable. Add
// new credentials to both lists below when they are added to Config.
func TestObfuscated_RedactsAllCredentials(t *testing.T) {
	c := Config{}
	c.Datastores.Postgres.Password = "runtime-secret"
	c.Datastores.Postgres.MigratePassword = "migrate-secret"
	c.Datastores.Postgres.ReaperPassword = "reaper-secret"
	c.Auth.SigningKey = "signing-secret"
	c.Bootstrap.Admin.Password = "admin-secret"
	c.APIToken.HMACKey = "pepper-secret"
	c.Ingest.Webhook.SharedSecret = "ingest-secret"
	c.Ingest.NATS.Token = "nats-secret"
	c.Runtime.HealthSnapshotToken = "health-secret"

	o := c.Obfuscated()

	checks := map[string]string{
		"postgres.password":             o.Datastores.Postgres.Password,
		"postgres.migrate_password":     o.Datastores.Postgres.MigratePassword,
		"postgres.reaper_password":      o.Datastores.Postgres.ReaperPassword,
		"auth.signing_key":              o.Auth.SigningKey,
		"bootstrap.admin.password":      o.Bootstrap.Admin.Password,
		"api_token.hmac_key":            o.APIToken.HMACKey,
		"ingest.webhook.shared_secret":  o.Ingest.Webhook.SharedSecret,
		"ingest.nats.token":             o.Ingest.NATS.Token,
		"runtime.health_snapshot_token": o.Runtime.HealthSnapshotToken,
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

func TestObfuscated_RedactsTheRabbitMQPassword(t *testing.T) {
	cases := []struct{ name, url, want string }{
		{"password", "amqp://paladin:hunter2@rabbit:5672/", "amqp://paladin:" + Redacted + "@rabbit:5672/"},
		{"no password", "amqp://rabbit:5672/", "amqp://rabbit:5672/"},
		{"unset", "", ""},
		{"unparseable", "amqp://paladin:hunter2@rabbit:port/", Redacted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Config{}
			c.Ingest.RabbitMQ.URL = tc.url
			if got := c.Obfuscated().Ingest.RabbitMQ.URL; got != tc.want {
				t.Errorf("url = %q, want %q", got, tc.want)
			}
		})
	}
}
