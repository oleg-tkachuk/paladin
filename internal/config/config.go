package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/utils"

	_ "embed"

	"cuelang.org/go/cue/cuecontext"
	"github.com/jackc/pgx/v5"
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
		resolver := NewK8sSecretResolver()
		if err := resolver.ResolveConfig(context.Background(), &cfg); err != nil {
			return Config{}, fmt.Errorf("secret resolution failed: %w", err)
		}
	}

	// Parse sizes
	if n, err := utils.ParseSizeString(cfg.Datastores.S3.PartSizeRaw); err == nil {
		cfg.Datastores.S3.PartSizeBytes = n
	} else {
		return Config{}, fmt.Errorf("failed to parse s3.part_size (%s): %w", cfg.Datastores.S3.PartSizeRaw, err)
	}

	if n, err := utils.ParseSizeString(cfg.Policy.MaxObjectSizeRaw); err == nil {
		cfg.Policy.MaxObjectSizeBytes = n
	} else {
		return Config{}, fmt.Errorf("failed to parse policy.max_object_size (%s): %w", cfg.Policy.MaxObjectSizeRaw, err)
	}

	if n, err := utils.ParseSizeString(cfg.Policy.MaxMultipartSizeRaw); err == nil {
		cfg.Policy.MaxMultipartSizeBytes = n
	} else {
		return Config{}, fmt.Errorf("failed to parse policy.max_multipart_size (%s): %w", cfg.Policy.MaxMultipartSizeRaw, err)
	}

	if n, err := utils.ParseSizeString(cfg.Policy.MinPartSizeRaw); err == nil {
		cfg.Policy.MinPartSizeBytes = n
	} else {
		return Config{}, fmt.Errorf("failed to parse policy.min_part_size (%s): %w", cfg.Policy.MinPartSizeRaw, err)
	}

	if n, err := utils.ParseSizeString(cfg.Policy.MaxPartSizeRaw); err == nil {
		cfg.Policy.MaxPartSizeBytes = n
	} else {
		return Config{}, fmt.Errorf("failed to parse policy.max_part_size (%s): %w", cfg.Policy.MaxPartSizeRaw, err)
	}

	log.Info("Config loaded and validated", zap.Any("config", cfg.Obfuscated()))

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

	if c.Datastores.S3.AccessKey != "" && c.Datastores.S3.AccessKeySecret != nil {
		return fmt.Errorf("s3: cannot specify both access_key and access_key_secret")
	}

	if c.Datastores.S3.SecretKey != "" && c.Datastores.S3.SecretKeySecret != nil {
		return fmt.Errorf("s3: cannot specify both secret_key and secret_key_secret")
	}

	return nil
}

func (c *Config) Sanitize() domain.SystemConfig {
	var pgHost, pgPort, pgUser, pgDB, pgSSLMode string
	pgConfig, err := pgx.ParseConfig(c.Datastores.Postgres.DSN)
	if err == nil {
		pgHost = pgConfig.Host
		pgPort = fmt.Sprintf("%d", pgConfig.Port)
		pgUser = pgConfig.User
		pgDB = pgConfig.Database
		if val, ok := pgConfig.RuntimeParams["sslmode"]; ok {
			pgSSLMode = val
		} else if pgConfig.TLSConfig == nil {
			pgSSLMode = "disable"
		} else {
			pgSSLMode = "enable"
		}
	}

	sc := domain.SystemConfig{}
	sc.App.Name = c.App.Name
	sc.App.Env = c.App.Env

	sc.Server.Name = c.Server.Name
	sc.Server.Mode = c.Server.Mode
	sc.Server.HTTP.Addr = c.Server.HTTP.Addr
	sc.Server.HTTP.CORSAllowedOrigins = c.Server.HTTP.CORSAllowedOrigins
	sc.Server.HTTP.ReadTimeout = c.Server.HTTP.ReadTimeout.String()
	sc.Server.HTTP.WriteTimeout = c.Server.HTTP.WriteTimeout.String()
	sc.Server.HTTP.RequestIDHeader = c.Server.HTTP.RequestIDHeader

	sc.Datastores.Postgres.Host = pgHost
	sc.Datastores.Postgres.Port = pgPort
	sc.Datastores.Postgres.User = pgUser
	sc.Datastores.Postgres.Dbname = pgDB
	sc.Datastores.Postgres.SslMode = pgSSLMode

	sc.Datastores.S3.Bucket = c.Datastores.S3.Bucket
	sc.Datastores.S3.Endpoint = c.Datastores.S3.Endpoint
	sc.Datastores.S3.PublicEndpoint = c.Datastores.S3.PublicEndpoint
	sc.Datastores.S3.ForcePathStyle = c.Datastores.S3.ForcePathStyle
	sc.Datastores.S3.PresignTTL = c.Datastores.S3.PresignTTL.String()
	sc.Datastores.S3.PartSize = c.Datastores.S3.PartSizeRaw
	sc.Datastores.S3.SSEType = c.Datastores.S3.SSEType

	sc.Policy.MaxObjectSize = c.Policy.MaxObjectSizeRaw
	sc.Policy.MaxMultipartSize = c.Policy.MaxMultipartSizeRaw
	sc.Policy.MinPartSize = c.Policy.MinPartSizeRaw
	sc.Policy.MaxPartSize = c.Policy.MaxPartSizeRaw
	sc.Policy.PresignPutTTL = c.Policy.PresignPutTTL.String()
	sc.Policy.PresignGetTTL = c.Policy.PresignGetTTL.String()
	sc.Policy.AllowedContentTypes = c.Policy.AllowedContentTypes

	sc.Auth.Enabled = c.Auth.Enabled

	sc.Security.TrustTenantIDFromRequest = c.Security.TrustTenantIDFromRequest
	sc.Security.RejectTenantMismatch = c.Security.RejectTenantMismatch
	sc.Security.EnableRLS = c.Security.EnableRLS

	sc.Housekeeping.EnableReaper = c.Housekeeping.EnableReaper
	sc.Housekeeping.PendingTTL = c.Housekeeping.PendingTTL.String()
	sc.Housekeeping.MultipartTTL = c.Housekeeping.MultipartTTL.String()
	sc.Housekeeping.GCInterval = c.Housekeeping.GCInterval.String()

	sc.RateLimit.RequestsPerSecond = float32(c.RateLimit.RequestsPerSecond)
	sc.RateLimit.Burst = c.RateLimit.Burst
	sc.RateLimit.MaxTenants = c.RateLimit.MaxTenants

	sc.Cache.Enabled = c.Cache.Enabled
	sc.Cache.MaxSize = c.Cache.MaxSize
	sc.Cache.TTL = c.Cache.TTL.String()

	sc.Timeouts.FastOperation = c.Timeouts.FastOperation.String()
	sc.Timeouts.DefaultOperation = c.Timeouts.DefaultOperation.String()
	sc.Timeouts.S3Operation = c.Timeouts.S3Operation.String()
	sc.Timeouts.LongOperation = c.Timeouts.LongOperation.String()

	sc.Idempotency.Enabled = c.Idempotency.Enabled
	sc.Idempotency.TTL = c.Idempotency.TTL.String()

	sc.OTel.Enabled = c.OTel.Enabled
	sc.OTel.Endpoint = c.OTel.Endpoint
	sc.OTel.Protocol = c.OTel.Protocol
	sc.OTel.Insecure = c.OTel.Insecure

	return sc
}
