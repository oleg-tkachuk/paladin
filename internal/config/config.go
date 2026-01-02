package config

import (
	"fmt"
	"os"

	"paladin/internal/utils"

	_ "embed"

	"cuelang.org/go/cue/cuecontext"
	cueyaml "cuelang.org/go/encoding/yaml"
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

	yamlBytes, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("YAML read error (%s): %w", path, err)
	}

	yamlFile, err := cueyaml.Extract(path, yamlBytes)
	if err != nil {
		return Config{}, fmt.Errorf("YAML -> CUE AST error: %w", err)
	}

	yamlVal := ctx.BuildFile(yamlFile)

	combined := schemaVal.Unify(yamlVal)
	if err := combined.Validate(); err != nil {
		return Config{}, fmt.Errorf("YAML validation failed (%s): %w", path, err)
	}

	var cfg Config
	// Encode back to YAML to apply defaults and luego unmarshal with goyaml
	// to support time.Duration and other types goyaml handles better.
	finalYAML, err := cueyaml.Encode(combined)
	if err != nil {
		return Config{}, fmt.Errorf("CUE -> YAML encoding failed: %w", err)
	}

	if err := goyaml.Unmarshal(finalYAML, &cfg); err != nil {
		return Config{}, fmt.Errorf("YAML unmarshal failed: %w", err)
	}

	// Parse sizes
	if n, err := utils.ParseSizeString(cfg.S3.PartSizeRaw); err == nil {
		cfg.S3.PartSizeBytes = n
	} else {
		return Config{}, fmt.Errorf("failed to parse s3.part_size (%s): %w", cfg.S3.PartSizeRaw, err)
	}

	if n, err := utils.ParseSizeString(cfg.Policy.MaxObjectSizeRaw); err == nil {
		cfg.Policy.MaxObjectSizeBytes = n
	} else {
		return Config{}, fmt.Errorf("failed to parse policy.max_object_size (%s): %w", cfg.Policy.MaxObjectSizeRaw, err)
	}

	log.Info("Config loaded", zap.String("path", path))

	return cfg, nil
}
