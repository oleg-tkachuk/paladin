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

	// minimalConfigYAML (drift_test.go) is the shared definition of
	// "only the fields schema.cue declares without a default". Sharing it
	// means a new no-default field breaks both tests at once, instead of
	// this one drifting quietly the way its stale `server:` block did.
	yamlInput := []byte(minimalConfigYAML)

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
	if cfg.Runtime.ShutdownTimeout.String() != "20s" {
		t.Errorf("Server.ShutdownTimeout: got %v want 20s", cfg.Runtime.ShutdownTimeout)
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

	// The host-run data plane listens on 8083 because another local service
	// owns 8080; the MCP bridge's default upstream must follow it.
	const (
		wantDataAddr = "0.0.0.0:8083"
		wantDataURL  = "http://localhost:8083"
	)
	if cfg.API.Server.Data.Addr != wantDataAddr {
		t.Errorf("API.Server.Data.Addr: got %q want %q", cfg.API.Server.Data.Addr, wantDataAddr)
	}
	if cfg.MCP.Upstreams.DataURL != wantDataURL {
		t.Errorf("MCP.Upstreams.DataURL: got %q want %q", cfg.MCP.Upstreams.DataURL, wantDataURL)
	}
}

// TestCueSchemaAcceptsEveryEnvTheCodeKnows keeps app.env's enum in schema.cue
// in step with the environment names the Go code gives meaning to. The schema
// once listed three of them, so an overlay that set app.env: dev — a name the
// weak-secret gate treats as disposable — was refused before the gate ran.
func TestCueSchemaAcceptsEveryEnvTheCodeKnows(t *testing.T) {
	ctx := cuecontext.New()
	schemaVal := ctx.CompileString(cueSchema)
	if schemaVal.Err() != nil {
		t.Fatalf("CUE schema invalid: %v", schemaVal.Err())
	}
	validate := func(env string) error {
		var doc map[string]any
		if err := goyaml.Unmarshal([]byte(minimalConfigYAML), &doc); err != nil {
			t.Fatal(err)
		}
		doc["app"].(map[string]any)["env"] = env
		raw, err := goyaml.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		file, err := cueyaml.Extract("env.yaml", raw)
		if err != nil {
			t.Fatal(err)
		}
		return schemaVal.Unify(ctx.BuildFile(file)).Validate()
	}

	known := []string{"staging", "prod"}
	for env := range disposableEnvs {
		if env != "" {
			known = append(known, env)
		}
	}
	for _, env := range known {
		if err := validate(env); err != nil {
			t.Errorf("app.env %q is handled by the code but refused by schema.cue: %v", env, err)
		}
	}
	// An empty env is not a choice: it is an overlay that forgot to pick one.
	if err := validate(""); err == nil {
		t.Error(`app.env "" was accepted; it must name an environment`)
	}
}
