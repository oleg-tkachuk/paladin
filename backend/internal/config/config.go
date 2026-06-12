package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/oleg-tkachuk/paladin/internal/utils"

	_ "embed"

	"cuelang.org/go/cue/cuecontext"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
	"go.uber.org/zap"
	goyaml "gopkg.in/yaml.v3"
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

	// Load environment variables prefixed with PALADIN_ and replace _ with .
	if err := k.Load(env.Provider(EnvPrefix, ".", func(s string) string {
		return strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(s, EnvPrefix)), "_", ".")
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

	// Parse sizes — per-backend part_size lands in PartSizeBytes on each entry.
	for name, b := range cfg.Storage.Backends {
		if b.PartSizeRaw == "" {
			continue
		}
		n, err := utils.ParseSizeString(b.PartSizeRaw)
		if err != nil {
			return Config{}, fmt.Errorf("storage.backends.%s.part_size (%s): %w", name, b.PartSizeRaw, err)
		}
		b.PartSizeBytes = n
		cfg.Storage.Backends[name] = b
	}

	if n, err := utils.ParseSizeString(cfg.Limits.MaxObjectSizeRaw); err == nil {
		cfg.Limits.MaxObjectSizeBytes = n
	} else {
		return Config{}, fmt.Errorf("failed to parse limits.max_object_size (%s): %w", cfg.Limits.MaxObjectSizeRaw, err)
	}

	if n, err := utils.ParseSizeString(cfg.Limits.MaxMultipartSizeRaw); err == nil {
		cfg.Limits.MaxMultipartSizeBytes = n
	} else {
		return Config{}, fmt.Errorf("failed to parse limits.max_multipart_size (%s): %w", cfg.Limits.MaxMultipartSizeRaw, err)
	}

	if n, err := utils.ParseSizeString(cfg.Limits.MinPartSizeRaw); err == nil {
		cfg.Limits.MinPartSizeBytes = n
	} else {
		return Config{}, fmt.Errorf("failed to parse limits.min_part_size (%s): %w", cfg.Limits.MinPartSizeRaw, err)
	}

	if n, err := utils.ParseSizeString(cfg.Limits.MaxPartSizeRaw); err == nil {
		cfg.Limits.MaxPartSizeBytes = n
	} else {
		return Config{}, fmt.Errorf("failed to parse limits.max_part_size (%s): %w", cfg.Limits.MaxPartSizeRaw, err)
	}

	log.Info("config loaded", zap.Any("config", cfg.Obfuscated()))

	return cfg, nil
}

func (c *Config) Validate() error {
	// Validate required fields
	if c.Datastores.Postgres.DSN == "" {
		return fmt.Errorf("postgres DSN is required")
	}

	if c.App.Name == "" {
		return fmt.Errorf("app name is required")
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
	if c.Auth.SigningKey != "" && c.Auth.SigningKeySecret != nil {
		return fmt.Errorf("auth: cannot specify both signing_key and signing_key_secret")
	}
	if c.Bootstrap.Admin.Enabled {
		if c.Bootstrap.Admin.Password != "" && c.Bootstrap.Admin.PasswordSecret != nil {
			return fmt.Errorf("bootstrap.admin: cannot specify both password and password_secret")
		}
	}

	if c.Storage.DefaultBackend != "" {
		if _, ok := c.Storage.Backends[c.Storage.DefaultBackend]; !ok {
			return fmt.Errorf("storage: default_backend %q not present in storage.backends", c.Storage.DefaultBackend)
		}
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
