package config

import (
	"testing"

	"cuelang.org/go/cue/cuecontext"
	cueyaml "cuelang.org/go/encoding/yaml"
	goyaml "gopkg.in/yaml.v3"
)

func TestCueSchema(t *testing.T) {
	ctx := cuecontext.New()
	schemaVal := ctx.CompileString(cueSchema)
	if schemaVal.Err() != nil {
		t.Fatalf("CUE schema invalid: %v", schemaVal.Err())
	}

	// Verify defaults with minimal concrete config
	concreteVal := ctx.CompileString(`
server: {
  name: "test-app"
  http: addr: ":8080"
  grpc: addr: ":9090"
}
postgres: dsn: "host=localhost"
s3: {
  bucket: "test"
  region: "us-east-1"
  endpoint: "http://localhost"
  access_key: "a"
  secret_key: "b"
}
otel: {
  service_name: "test"
  environment: "test"
  otlp_endpoint: "test"
}
`)
	combined := schemaVal.Unify(concreteVal)

	var cfg Config
	// Use same logic as config.Load
	finalYAML, err := cueyaml.Encode(combined)
	if err != nil {
		t.Fatalf("CUE -> YAML encoding failed: %v", err)
	}

	if err := goyaml.Unmarshal(finalYAML, &cfg); err != nil {
		t.Fatalf("YAML unmarshal failed: %v", err)
	}

	if cfg.Postgres.MaxConns != 20 {
		t.Errorf("Expected MaxConns 20, got %d", cfg.Postgres.MaxConns)
	}
}
