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
	yamlInput := []byte(`
app:
  name: "test-app"
  env: "local"
server:
  name: "test-app"
  http:
    addr: ":8080"
datastores:
  postgres:
    dsn: "host=localhost"
  s3:
    objectKey: "test"
    region: "us-east-1"
    endpoint: "http://localhost"
    access_key: "a"
    secret_key: "b"
otel:
  endpoint: "test"
  resource:
    service.name: "test"
    deployment.environment: "test"
auth:
  oidc:
    issuer_url: "http://issuer"
    audience: "aud"
`)

	yamlFile, err := cueyaml.Extract("test.yaml", yamlInput)
	if err != nil {
		t.Fatalf("YAML -> CUE AST error: %v", err)
	}

	concreteVal := ctx.BuildFile(yamlFile)
	combined := schemaVal.Unify(concreteVal)
	if err = combined.Validate(); err != nil {
		t.Fatalf("CUE validation failed: %v", err)
	}

	var cfg Config
	// Use JSON intermediate to support time.Duration and avoid CUE encoding issues
	jsonBytes, err := combined.MarshalJSON()
	if err != nil {
		t.Fatalf("CUE -> JSON marshaling failed: %v", err)
	}

	if err := goyaml.Unmarshal(jsonBytes, &cfg); err != nil {
		t.Fatalf("JSON -> YAML unmarshal failed: %v", err)
	}

	if cfg.Datastores.Postgres.Pool.MaxConns != 20 {
		t.Errorf("Expected MaxConns 20, got %d", cfg.Datastores.Postgres.Pool.MaxConns)
	}
}
