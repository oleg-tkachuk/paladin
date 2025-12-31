package config

import (
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

func Load(path string, log *zap.Logger) Config {
	ctx := cuecontext.New()

	schemaVal := ctx.CompileString(cueSchema)
	if schemaVal.Err() != nil {
		log.Fatal("CUE schema invalid", zap.Error(schemaVal.Err()))
	}

	yamlBytes, err := os.ReadFile(path)
	if err != nil {
		log.Fatal("YAML read error", zap.String("path", path), zap.Error(err))
	}

	yamlFile, err := cueyaml.Extract(path, yamlBytes)
	if err != nil {
		log.Fatal("YAML -> CUE AST error", zap.Error(err))
	}

	yamlVal := ctx.BuildFile(yamlFile)

	combined := schemaVal.Unify(yamlVal)
	if err := combined.Validate(); err != nil {
		log.Fatal("YAML validation failed", zap.String("path", path), zap.Error(err))
	}

	var cfg Config
	if err := goyaml.Unmarshal(yamlBytes, &cfg); err != nil {
		log.Fatal("YAML unmarshal failed", zap.Error(err))
	}

	// Parse sizes
	if n, err := utils.ParseSizeString(cfg.S3.PartSizeRaw); err == nil {
		cfg.S3.PartSizeBytes = n
	} else {
		log.Fatal("Failed to parse s3.part_size", zap.Error(err))
	}

	if n, err := utils.ParseSizeString(cfg.Policy.MaxObjectSizeRaw); err == nil {
		cfg.Policy.MaxObjectSizeBytes = n
	} else {
		log.Fatal("Failed to parse policy.max_object_size", zap.Error(err))
	}

	log.Info("Config loaded", zap.String("path", path))

	return cfg
}
