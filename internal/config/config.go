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

func Load(path string, log *zap.Logger) (Config, error) {
	ctx := cuecontext.New()

	schemaVal := ctx.CompileString(cueSchema)
	if schemaVal.Err() != nil {
		return Config{}, fmt.Errorf("CUE schema invalid: %w", schemaVal.Err())
	}

	// Initialize koanf
	k := koanf.New(".")

	// Load configuration from YAML file
	if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
		return Config{}, fmt.Errorf("YAML read error (%s): %w", path, err)
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
		return Config{}, fmt.Errorf("config validation failed (%s): %w", path, err)
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

	// Validate secret mutual exclusivity
	if c.Datastores.Postgres.Password != "" && c.Datastores.Postgres.PasswordSecret != nil {
		return fmt.Errorf("postgres: cannot specify both password and password_secret")
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
		if b.SSE.Type == "aws:kms" && b.SSE.KeyID == "" {
			return fmt.Errorf("storage.backends.%s: sse.key_id required when sse.type=aws:kms", name)
		}
		if err := validateBackendAuth(name, b); err != nil {
			return err
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
