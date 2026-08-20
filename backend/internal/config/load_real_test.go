package config

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
	goyaml "gopkg.in/yaml.v3"
)

// TestHelmValuesConfigBlock pins the config: subtree of the chart values.yaml
// against the live CUE schema. Helm bakes this subtree into the ConfigMap
// the pod reads, so any drift between configs/config.yaml and the chart
// breaks every cluster deploy.
//
// The bare values.yaml is intentionally prod-safe (empty app.env, empty
// signing_key, empty storage endpoints) — every env overlay supplies
// the deltas. So we validate the merge values.yaml + values-local.yaml,
// which is the one overlay holding concrete (non-placeholder) secrets
// suitable for the CUE schema's min-length / URL-format constraints.
// dev/staging/prod overlays carry `<placeholder>` tokens that get
// replaced by SealedSecrets / external-secrets at deploy time and would
// fail Validate() here by design.
func TestHelmValuesConfigBlock(t *testing.T) {
	extract := func(srcPath, dstPath string) {
		body, err := os.ReadFile(srcPath)
		if err != nil {
			t.Fatalf("read %s: %v", srcPath, err)
		}
		var wrap struct {
			Config map[string]any `yaml:"config"`
		}
		if err := goyaml.Unmarshal(body, &wrap); err != nil {
			t.Fatalf("decode %s: %v", srcPath, err)
		}
		out, err := goyaml.Marshal(wrap.Config)
		if err != nil {
			t.Fatalf("re-marshal %s: %v", srcPath, err)
		}
		if err := os.WriteFile(dstPath, out, 0o600); err != nil {
			t.Fatalf("write %s: %v", dstPath, err)
		}
	}
	dir := t.TempDir()
	base := filepath.Join(dir, "base.yaml")
	overlay := filepath.Join(dir, "local.yaml")
	extract("../../deploy/chart/values.yaml", base)
	extract("../../deploy/chart/values-local.yaml", overlay)
	if _, err := Load([]string{base, overlay}, zap.NewNop()); err != nil {
		t.Fatalf("Load chart values.yaml + values-local.yaml -> .config: %v", err)
	}
}

// TestLoadMinimalYAML asserts that a YAML supplying only the no-default
// fields (postgres dsn, app.name, one storage backend, signing key) loads
// cleanly — every other field falls back to CUE defaults. Catches the
// "incomplete value: cannot convert string to JSON" failure mode.
func TestLoadMinimalYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "min.yaml")
	body := []byte(`
app:
  name: paladin
  env: local
datastores:
  postgres:
    dsn: "postgres://localhost/test"
auth:
  signing_key: "dev-secret-change-me-32-bytes-min"
storage:
  backends:
    primary: {}
`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := Load([]string{path}, zap.NewNop())
	if err != nil {
		t.Fatalf("Load minimal yaml: %v", err)
	}
	// Defaults must have populated every three-plane addr.
	if cfg.API.Server.Data.Addr == "" || cfg.Admin.Server.Addr == "" || cfg.API.Server.IAM.Addr == "" {
		t.Errorf("three-plane addrs not defaulted: %+v", cfg.Runtime)
	}
	if cfg.API.Server.Data.Addr == cfg.Admin.Server.Addr {
		t.Errorf("data and admin defaulted to the same addr: %q", cfg.API.Server.Data.Addr)
	}
	// Storage backend disjunctions must default — no static-keys credentials
	// supplied, so default_chain is the only auth.mode that won't be rejected
	// by Validate().
	be := cfg.Storage.Backends["primary"]
	if be.Kind == "" {
		t.Error("storage.backends[primary].kind not defaulted")
	}
	if be.Auth.Mode == "" {
		t.Error("storage.backends[primary].auth.mode not defaulted")
	}
}

// TestLoadRealConfigYAML loads the canonical configs/config.yaml end-to-end
// (CUE validation + Go struct decode + Validate). This catches drift
// between the example file and the schema before deploy time.
func TestLoadRealConfigYAML(t *testing.T) {
	for _, path := range []string{
		"../../configs/config.yaml",
		"../../configs/local.yaml",
	} {
		path := path
		t.Run(path, func(t *testing.T) {
			if _, err := Load([]string{path}, zap.NewNop()); err != nil {
				t.Fatalf("Load %s: %v", path, err)
			}
		})
	}
	cfg, err := Load([]string{"../../configs/config.yaml"}, zap.NewNop())
	if err != nil {
		t.Fatalf("Load configs/config.yaml: %v", err)
	}
	if cfg.App.Name == "" {
		t.Error("App.Name is empty after load")
	}
	if cfg.Datastores.Postgres.DSN == "" {
		t.Error("Postgres.DSN is empty after load")
	}
	if !cfg.Middleware.RateLimit.Enabled {
		t.Error("Middleware.RateLimit.Enabled: expected true from yaml")
	}
	// ADR-0010 Phase 1: the canonical Collection EUID is enabled in the shipped
	// config. Guards against the flag silently reverting to the Go zero-value.
	if !cfg.Cedar.CanonicalCollectionEUID {
		t.Error("Cedar.CanonicalCollectionEUID: expected true from configs/config.yaml (ADR-0010 Phase 1)")
	}
	if cfg.MCP.HTTP.Addr == "" {
		t.Error("MCP.HTTP.Addr is empty")
	}
	if cfg.Auth.AccessTokenTTL == 0 {
		t.Error("Auth.AccessTokenTTL is zero")
	}
	if _, ok := cfg.Storage.Backends["primary"]; !ok {
		t.Error("Storage.Backends[primary] missing")
	}
	if cfg.API.Server.Data.Addr == "" || cfg.Admin.Server.Addr == "" || cfg.API.Server.IAM.Addr == "" {
		t.Error("three-plane HTTP addrs incomplete")
	}
	// service / env identity must propagate from app.* to logger.fields and
	// otel.resource via CUE defaults — the YAML omits them.
	if cfg.Logger.Fields.Service != cfg.App.Name {
		t.Errorf("logger.fields.service: got %q want %q (default from app.name)",
			cfg.Logger.Fields.Service, cfg.App.Name)
	}
	if cfg.Logger.Fields.Env != cfg.App.Env {
		t.Errorf("logger.fields.env: got %q want %q (default from app.env)",
			cfg.Logger.Fields.Env, cfg.App.Env)
	}
	if cfg.OTel.Resource.ServiceName != cfg.App.Name {
		t.Errorf("otel.resource.service.name: got %q want %q",
			cfg.OTel.Resource.ServiceName, cfg.App.Name)
	}
	if cfg.OTel.Resource.DeploymentEnvironment != cfg.App.Env {
		t.Errorf("otel.resource.deployment.environment: got %q want %q",
			cfg.OTel.Resource.DeploymentEnvironment, cfg.App.Env)
	}
	// Every worker subsystem must have a non-zero interval after CUE
	// applies its defaults — silent-zero would hot-loop the goroutine.
	if cfg.Worker.Jobs.RefreshTokenReap.Interval == 0 {
		t.Error("workers.refresh_token_reap.interval defaulted to zero")
	}
	if cfg.Worker.Jobs.Lifecycle.Interval == 0 {
		t.Error("workers.lifecycle.interval defaulted to zero")
	}
	if cfg.Worker.Jobs.Replication.Interval == 0 || cfg.Worker.Jobs.Replication.LookbackWindow == 0 {
		t.Error("workers.replication interval/lookback_window defaulted to zero")
	}
}
