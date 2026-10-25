package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/bytesize"

	_ "embed"

	"cuelang.org/go/cue/cuecontext"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
	"go.uber.org/zap"
	goyaml "gopkg.in/yaml.v3"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
)

//go:embed schema.cue
var cueSchema string

// Load reads one or more YAML files in order and merges them — later
// files override earlier ones, key-by-key, deep-merge style. The
// minimum is a single fully-populated config (the legacy shape);
// the overlay mode lets a base.yaml carry the full schema and a
// per-environment overlay file carry just the deltas.
//
// Loading order:
//
//  1. paths[0] (base, must contain every required field).
//  2. paths[1..] (overlays, may contain partial trees that override).
//  3. Environment variables prefixed with PALADIN_ override everything.
//  4. CUE schema unification injects defaults for unset fields.
//
// Strict-key validation runs against the merged result so an overlay
// file that intentionally omits big swaths of the schema isn't
// rejected for missing keys it never tried to set.
func Load(paths []string, log *zap.Logger) (Config, error) {
	if len(paths) == 0 {
		return Config{}, fmt.Errorf("config: at least one path required")
	}

	ctx := cuecontext.New()

	schemaVal := ctx.CompileString(cueSchema)
	if schemaVal.Err() != nil {
		return Config{}, fmt.Errorf("CUE schema invalid: %w", schemaVal.Err())
	}

	// Initialize koanf
	k := koanf.New(".")

	// Load every path in order. Later files merge over earlier ones
	// at the koanf-key level (deep-merge), so an overlay setting
	// `app.env: prod` overrides only that key.
	for _, path := range paths {
		if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
			return Config{}, fmt.Errorf("YAML read error (%s): %w", path, err)
		}
	}

	// Strict-key check after the merge so an overlay carrying only
	// deltas isn't rejected for fields it didn't touch. The base
	// file's full key set still gets validated; the overlay's keys
	// are validated against the merged set.
	if err := validateNoUnknownKeysInMap(k.Raw()); err != nil {
		return Config{}, err
	}

	// Load environment variables prefixed with PALADIN_. The variable name is
	// resolved against the schema rather than transliterated — see
	// EnvKeyMapper for why a blind `_` → `.` silently drops every setting
	// whose own name contains an underscore.
	envKeys := EnvKeyMapper(KnownConfigPaths())
	envVals := EnvValueParser(KnownConfigKinds())
	if err := k.Load(env.Provider(".", env.Opt{
		Prefix: EnvPrefix,
		TransformFunc: func(key, val string) (string, any) {
			path := envKeys(key)
			return path, envVals(path, val)
		},
	}), nil); err != nil {
		return Config{}, fmt.Errorf("failed to load env vars: %w", err)
	}

	// Export merged config back to JSON for CUE validation and default injection
	configBytes, err := json.Marshal(k.Raw())
	if err != nil {
		return Config{}, fmt.Errorf("failed to marshal merged config: %w", err)
	}

	configVal := ctx.CompileBytes(configBytes)
	combined := schemaVal.Unify(configVal)
	if err := combined.Validate(); err != nil {
		return Config{}, fmt.Errorf("config validation failed (%s): %w", strings.Join(paths, ", "), err)
	}

	var cfg Config

	jsonBytes, err := combined.MarshalJSON()
	if err != nil {
		return Config{}, fmt.Errorf("CUE -> JSON marshaling failed: %w", err)
	}

	if err := goyaml.Unmarshal(jsonBytes, &cfg); err != nil {
		return Config{}, fmt.Errorf("YAML unmarshal failed: %w", err)
	}

	// Parse sizes — per-backend part_size lands in PartSizeBytes on each entry.
	for name, b := range cfg.Storage.Backends {
		if b.PartSizeRaw == "" {
			continue
		}
		n, err := bytesize.Parse(b.PartSizeRaw)
		if err != nil {
			return Config{}, fmt.Errorf("storage.backends.%s.part_size (%s): %w", name, b.PartSizeRaw, err)
		}
		b.PartSizeBytes = n
		cfg.Storage.Backends[name] = b
	}

	if n, err := bytesize.Parse(cfg.Limits.MaxObjectSizeRaw); err == nil {
		cfg.Limits.MaxObjectSizeBytes = n
	} else {
		return Config{}, fmt.Errorf("failed to parse limits.max_object_size (%s): %w", cfg.Limits.MaxObjectSizeRaw, err)
	}

	if n, err := bytesize.Parse(cfg.Limits.MaxMultipartSizeRaw); err == nil {
		cfg.Limits.MaxMultipartSizeBytes = n
	} else {
		return Config{}, fmt.Errorf("failed to parse limits.max_multipart_size (%s): %w", cfg.Limits.MaxMultipartSizeRaw, err)
	}

	if n, err := bytesize.Parse(cfg.Limits.MinPartSizeRaw); err == nil {
		cfg.Limits.MinPartSizeBytes = n
	} else {
		return Config{}, fmt.Errorf("failed to parse limits.min_part_size (%s): %w", cfg.Limits.MinPartSizeRaw, err)
	}

	if n, err := bytesize.Parse(cfg.Limits.MaxPartSizeRaw); err == nil {
		cfg.Limits.MaxPartSizeBytes = n
	} else {
		return Config{}, fmt.Errorf("failed to parse limits.max_part_size (%s): %w", cfg.Limits.MaxPartSizeRaw, err)
	}

	// Validate after the sizes are parsed: the upload limits are checked in
	// bytes.
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("configuration validation failed: %w", err)
	}

	// Resolve secrets if running in a Kubernetes environment
	if os.Getenv(DefaultK8sServiceHostEnvKey) != "" {
		resolver := NewK8sSecretResolver(log)
		if err := resolver.ResolveConfig(context.Background(), &cfg); err != nil {
			return Config{}, fmt.Errorf("secret resolution failed: %w", err)
		}
	}

	if cfg.Security.LogSensitive {
		log.Warn(retiredLogSensitiveWarning)
	}
	log.Info("config loaded", zap.Any("config", cfg.Obfuscated()))

	return cfg, nil
}

// retiredLogSensitiveWarning is logged when security.log_sensitive is true.
const retiredLogSensitiveWarning = "security.log_sensitive is retired and has no effect: no secret is ever logged; remove it from the config"

func (c *Config) Validate() error {
	// Validate required fields
	if c.Datastores.Postgres.DSN == "" {
		return fmt.Errorf("postgres DSN is required")
	}
	if r := c.Datastores.Postgres.Replica; r.Enabled {
		if _, err := c.Datastores.Postgres.ReplicaDSN(); err != nil {
			return fmt.Errorf("postgres: %w", err)
		}
		if r.LagCheckPeriod <= 0 {
			return fmt.Errorf("postgres: replica.lag_check_period must be positive")
		}
		if r.MaxLag < 0 {
			return fmt.Errorf("postgres: replica.max_lag must not be negative")
		}
	}

	if c.App.Name == "" {
		return fmt.Errorf("app name is required")
	}

	// Surfaced-for-purge, never auto-purged: a zero or negative window would
	// mark every trashed tenant as due the moment it was trashed.
	if c.Admin.Tenants.TrashRetention <= 0 {
		return fmt.Errorf("admin.tenants.trash_retention must be positive")
	}

	// ── Secret mutual-exclusivity sweep ─────────────────────────────
	// For every field with a `<field>_secret` sibling, accepting both
	// inline + SecretRef is ambiguous: which one wins? We reject at
	// load time so the operator's mental model can't drift from
	// what the runtime actually uses. SecretRef is the production
	// path; inline is for dev convenience.
	if c.Datastores.Postgres.Password != "" && c.Datastores.Postgres.PasswordSecret != nil {
		return fmt.Errorf("postgres: cannot specify both password and password_secret")
	}
	if c.Datastores.Postgres.MigratePassword != "" && c.Datastores.Postgres.MigratePasswordSecret != nil {
		return fmt.Errorf("postgres: cannot specify both migrate_password and migrate_password_secret")
	}
	if c.Datastores.Postgres.ReaperPassword != "" && c.Datastores.Postgres.ReaperPasswordSecret != nil {
		return fmt.Errorf("postgres: cannot specify both reaper_password and reaper_password_secret")
	}
	if r := c.Datastores.Postgres.Replica; r.Password != "" && r.PasswordSecret != nil {
		return fmt.Errorf("postgres: cannot specify both replica.password and replica.password_secret")
	}
	if c.Auth.SigningKey != "" && c.Auth.SigningKeySecret != nil {
		return fmt.Errorf("auth: cannot specify both signing_key and signing_key_secret")
	}
	if err := validateMCPExchange(c.MCP.OAuth); err != nil {
		return fmt.Errorf("mcp.oauth.exchange: %w", err)
	}
	for _, sc := range c.Auth.OAuth.SeedClients {
		if err := validateSeedClient(sc); err != nil {
			return fmt.Errorf("auth.oauth.seed_clients[%s]: %w", sc.ClientID, err)
		}
	}
	if !limes.IsUnitCode(c.Capability.Currency) {
		return fmt.Errorf("capability.currency: %q is not an ISO 4217 code in upper case", c.Capability.Currency)
	}
	if c.Capability.DPoPNonce.Key != "" && c.Capability.DPoPNonce.KeySecret != nil {
		return fmt.Errorf("capability.dpop_nonce: cannot specify both key and key_secret")
	}
	if c.MCP.DPoPKey != "" && c.MCP.DPoPKeySecret != nil {
		return fmt.Errorf("mcp: cannot specify both dpop_key and dpop_key_secret")
	}
	if c.APIToken.HMACKey != "" && c.APIToken.HMACKeySecret != nil {
		return fmt.Errorf("api_token: cannot specify both hmac_key and hmac_key_secret")
	}
	if c.Ingest.Webhook.SharedSecret != "" && c.Ingest.Webhook.SharedSecretRef.Name != "" {
		return fmt.Errorf("ingest.webhook: cannot specify both shared_secret and shared_secret_ref")
	}
	if c.Ingest.NATS.Token != "" && c.Ingest.NATS.TokenRef.Name != "" {
		return fmt.Errorf("ingest.nats: cannot specify both token and token_ref")
	}
	if c.Ingest.RabbitMQ.URL != "" && c.Ingest.RabbitMQ.URLRef.Name != "" {
		return fmt.Errorf("ingest.rabbitmq: cannot specify both url and url_ref")
	}
	if c.Runtime.HealthSnapshotToken != "" && c.Runtime.HealthSnapshotTokenSecret != nil {
		return fmt.Errorf("runtime: cannot specify both health_snapshot_token and health_snapshot_token_secret")
	}
	if c.Bootstrap.Admin.Enabled {
		if c.Bootstrap.Admin.Password != "" && c.Bootstrap.Admin.PasswordSecret != nil {
			return fmt.Errorf("bootstrap.admin: cannot specify both password and password_secret")
		}
	}

	if err := c.MCP.Upstreams.Validate(); err != nil {
		return err
	}

	if err := c.validateCapabilityBounds(); err != nil {
		return err
	}
	if err := c.validateOAuthClientReap(); err != nil {
		return err
	}

	if err := c.Limits.Presign.Validate(); err != nil {
		return err
	}
	if err := c.Limits.UploadLimits().Validate(); err != nil {
		return err
	}

	for name, b := range c.Storage.Backends {
		if b.Auth.AccessKey != "" && b.Auth.AccessKeySecret != nil {
			return fmt.Errorf("storage.backends.%s.auth: cannot specify both access_key and access_key_secret", name)
		}
		if b.Auth.SecretKey != "" && b.Auth.SecretKeySecret != nil {
			return fmt.Errorf("storage.backends.%s.auth: cannot specify both secret_key and secret_key_secret", name)
		}
		if b.Auth.SessionToken != "" && b.Auth.SessionTokenSecret != nil {
			return fmt.Errorf("storage.backends.%s.auth: cannot specify both session_token and session_token_secret", name)
		}
		if b.SSE.Type == "aws:kms" && b.SSE.KeyID == "" {
			return fmt.Errorf("storage.backends.%s: sse.key_id required when sse.type=aws:kms", name)
		}
		if err := validateBackendAuth(name, b); err != nil {
			return err
		}
	}

	// ── Ingest webhook must be authenticated outside dev ────────────
	// The HMAC check is skipped when SharedSecret is empty (publisher
	// signs with ""). That's fine on a laptop, but in staging/prod an
	// unauthenticated receiver lets anyone who reaches the ingest port
	// forge PROMOTE events — and the ingest plane runs on a BYPASSRLS
	// pool, so a forged event mutates object state at the highest
	// privilege level. Fail fast rather than boot a wide-open receiver.
	if c.Ingest.Enabled && c.Ingest.Driver == "webhook" {
		devEnv := c.App.Env == "" || c.App.Env == "local" ||
			c.App.Env == "dev" || c.App.Env == "development"
		noSecret := c.Ingest.Webhook.SharedSecret == "" &&
			c.Ingest.Webhook.SharedSecretRef.Name == ""
		if !devEnv && noSecret {
			return fmt.Errorf(
				"ingest.webhook: shared_secret or shared_secret_ref is required when app.env=%q "+
					"(an unauthenticated webhook receiver lets anyone forge object events)",
				c.App.Env)
		}
	}

	// ── Committed-default credentials are public credentials ────────
	// Last, so an operator sees the structural errors above first: a
	// config that fails both is more likely mis-assembled than merely
	// under-secured.
	if err := validateNoWeakSecrets(c); err != nil {
		return err
	}

	return nil
}

// The capability keys validateCapabilityBounds names in its refusals.
const (
	revocationCacheTTLKey       = "revocation_cache_ttl"
	revocationCacheSyncedTTLKey = "revocation_cache_synced_ttl"
	chargesOlderThanKey         = "charges_older_than"
)

// validateCapabilityBounds holds the capability durations to the bounds limes
// refuses outside of, so a misconfigured value stops the boot rather than
// every verifier construction or every purge tick: a verifier leeway past
// limes.MaxLeeway, and a purge retention shorter than limes.MinPurgeRetention,
// which would drop a revocation while a verifier still honours its token, and
// a revocation cache TTL past limes.MaxRevocationCacheSyncedTTL.
func (c *Config) validateCapabilityBounds() error {
	if l := c.Capability.VerifierLeeway; l < 0 || l > limes.MaxLeeway {
		return fmt.Errorf("capability: verifier_leeway %s is outside 0..%s", l, limes.MaxLeeway)
	}
	// limes caches for the longer of the two, and refuses past its ceiling.
	for key, ttl := range map[string]time.Duration{
		revocationCacheTTLKey:       c.Capability.RevocationCacheTTL,
		revocationCacheSyncedTTLKey: c.Capability.RevocationCacheSyncedTTL,
	} {
		if ttl > limes.MaxRevocationCacheSyncedTTL {
			return fmt.Errorf("capability: %s %s is longer than %s", key, ttl, limes.MaxRevocationCacheSyncedTTL)
		}
	}
	if r := c.Worker.Jobs.Capability.ExpiredFor; r != 0 && r < limes.MinPurgeRetention {
		return fmt.Errorf("worker.jobs.capability: expired_for %s is shorter than %s", r, limes.MinPurgeRetention)
	}
	if j := c.Worker.Jobs.Capability; j.PurgeCharges && j.ChargesOlderThan < meter.MinChargeRetention {
		return fmt.Errorf("worker.jobs.capability: %s %s is shorter than %s", chargesOlderThanKey, j.ChargesOlderThan, meter.MinChargeRetention)
	}
	return nil
}

// validateOAuthClientReap refuses a running reaper with no age: every idle
// self-registered client would be removed the moment it registered.
func (c *Config) validateOAuthClientReap() error {
	if r := c.Worker.Jobs.OAuthClientReap; r.Interval > 0 && r.MaxAge <= 0 {
		return fmt.Errorf("worker.jobs.oauth_client_reap: max_age %s must be positive while interval is %s", r.MaxAge, r.Interval)
	}
	return nil
}

// validateBackendAuth enforces per-mode invariants on StorageBackendAuth.
// `auth.mode` is mandatory — explicit selection is required for every
// backend so the operator can't accidentally hit the wrong AWS credential
// chain at runtime.
func validateBackendAuth(name string, b StorageBackend) error {
	a := b.Auth
	switch a.Mode {
	case AuthModeStaticKeys:
		hasAK := a.AccessKey != "" || a.AccessKeySecret != nil
		hasSK := a.SecretKey != "" || a.SecretKeySecret != nil
		if !hasAK || !hasSK {
			return fmt.Errorf("storage.backends.%s.auth.mode=static_keys: access_key and secret_key are required", name)
		}
		if a.RoleARN != "" {
			return fmt.Errorf("storage.backends.%s.auth: role_arn is not valid with mode=static_keys", name)
		}
	case AuthModeDefaultChain:
		if a.AccessKey != "" || a.SecretKey != "" || a.AccessKeySecret != nil || a.SecretKeySecret != nil {
			return fmt.Errorf("storage.backends.%s.auth.mode=default_chain: must not specify static keys (use env vars or instance role)", name)
		}
		if a.RoleARN != "" {
			return fmt.Errorf("storage.backends.%s.auth: role_arn is not valid with mode=default_chain (use mode=assume_role)", name)
		}
	case AuthModeAssumeRole, AuthModeWebIdentity:
		if a.RoleARN == "" {
			return fmt.Errorf("storage.backends.%s.auth.mode=%s: role_arn is required", name, a.Mode)
		}
		if a.DurationSeconds < 0 {
			return fmt.Errorf("storage.backends.%s.auth.duration_seconds: must be ≥ 0", name)
		}
	case "":
		return fmt.Errorf("storage.backends.%s.auth.mode is required (one of static_keys|default_chain|assume_role|web_identity)", name)
	default:
		return fmt.Errorf("storage.backends.%s.auth.mode=%q: unknown (want static_keys|default_chain|assume_role|web_identity)", name, a.Mode)
	}
	return nil
}

// validateSeedClient refuses a seed client whose authentication cannot be
// what it says: a public client holds no secret, a confidential one holds
// exactly one, and only a confidential client may exchange tokens.
func validateSeedClient(sc OAuthSeedClient) error {
	hasSecret := sc.ClientSecret != "" || sc.ClientSecretRef != nil
	switch {
	case sc.ClientSecret != "" && sc.ClientSecretRef != nil:
		return errors.New("cannot specify both client_secret and client_secret_ref")
	case sc.Public && hasSecret:
		return errors.New("a public client takes no client_secret")
	case !sc.Public && !hasSecret:
		return errors.New("a confidential client needs client_secret or client_secret_ref")
	case sc.TokenExchange && sc.Public:
		return errors.New("token_exchange needs a confidential client")
	}
	return nil
}

// validateMCPExchange refuses an exchange the MCP server could not perform:
// one with no secret, two of them, or one with OAuth off at the edge.
func validateMCPExchange(o MCPOAuth) error {
	ex := o.Exchange
	if ex.ClientID == "" {
		if ex.ClientSecret != "" || ex.ClientSecretRef != nil {
			return errors.New("a client secret needs a client_id")
		}
		return nil
	}
	switch {
	case ex.ClientSecret != "" && ex.ClientSecretRef != nil:
		return errors.New("cannot specify both client_secret and client_secret_ref")
	case ex.ClientSecret == "" && ex.ClientSecretRef == nil:
		return errors.New("client_id needs client_secret or client_secret_ref")
	case !o.Enabled:
		return errors.New("needs mcp.oauth.enabled")
	}
	return nil
}
