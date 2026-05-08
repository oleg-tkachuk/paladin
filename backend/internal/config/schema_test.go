package config

import (
	"testing"

	"cuelang.org/go/cue/cuecontext"
	cueyaml "cuelang.org/go/encoding/yaml"
	goyaml "gopkg.in/yaml.v3"
)

// TestCueSchema verifies that a minimal concrete YAML — only the
// no-default fields — unifies with the CUE schema and that the resulting
// merged config carries the schema-declared defaults all the way through
// JSON marshalling into the Go Config struct.
//
// Any new no-default field added to schema.cue must be added here too,
// otherwise this test fails at validation time and surfaces the omission
// before the runtime loader silently rejects the YAML.
func TestCueSchema(t *testing.T) {
	ctx := cuecontext.New()
	schemaVal := ctx.CompileString(cueSchema)
	if schemaVal.Err() != nil {
		t.Fatalf("CUE schema invalid: %v", schemaVal.Err())
	}

	yamlInput := []byte(`
app:
  name: "test-app"
  env: "local"
server:
  data_http: { addr: ":8080" }
  admin_http: { addr: ":8090" }
  iam_http: { addr: ":8085" }
datastores:
  postgres:
    dsn: "postgres://localhost/test"
storage:
  backends:
    primary:
      kind: "s3-compatible"
      auth:
        mode: "static_keys"
        access_key: "a"
        secret_key: "b"
`)

	yamlFile, err := cueyaml.Extract("test.yaml", yamlInput)
	if err != nil {
		t.Fatalf("YAML -> CUE AST error: %v", err)
	}

	concreteVal := ctx.BuildFile(yamlFile)
	combined := schemaVal.Unify(concreteVal)
	if err := combined.Validate(); err != nil {
		t.Fatalf("CUE validation failed: %v", err)
	}

	jsonBytes, err := combined.MarshalJSON()
	if err != nil {
		t.Fatalf("CUE -> JSON marshaling failed: %v", err)
	}

	var cfg Config
	if err := goyaml.Unmarshal(jsonBytes, &cfg); err != nil {
		t.Fatalf("JSON -> Go unmarshal failed: %v", err)
	}

	// Spot-check defaults from across the schema.
	if cfg.Datastores.Postgres.Pool.MaxConns != 20 {
		t.Errorf("Postgres.Pool.MaxConns: got %d want 20", cfg.Datastores.Postgres.Pool.MaxConns)
	}
	if cfg.Server.ShutdownTimeout.String() != "20s" {
		t.Errorf("Server.ShutdownTimeout: got %v want 20s", cfg.Server.ShutdownTimeout)
	}
	if !cfg.Middleware.RateLimit.Enabled {
		t.Error("Middleware.RateLimit.Enabled: default should be true")
	}
	if cfg.Auth.AccessTokenTTL.String() != "15m0s" {
		t.Errorf("Auth.AccessTokenTTL: got %v want 15m", cfg.Auth.AccessTokenTTL)
	}
	if cfg.MCP.HTTP.Addr != ":8095" {
		t.Errorf("MCP.HTTP.Addr: got %q want :8095", cfg.MCP.HTTP.Addr)
	}
	if !cfg.MCP.Stdio.Enabled {
		t.Error("MCP.Stdio.Enabled: default should be true")
	}
}
