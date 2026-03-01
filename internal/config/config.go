package config

import (
	"encoding/json"
	"fmt"
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
	if err := k.Load(env.Provider("PALADIN_", ".", func(s string) string {
		return strings.Replace(strings.ToLower(strings.TrimPrefix(s, "PALADIN_")), "_", ".", -1)
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

	log.Info("Config loaded", zap.String("path", path))

	return cfg, nil
}
