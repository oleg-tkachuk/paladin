package config

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
	goyaml "gopkg.in/yaml.v3"
)

// TestHelmValuesConfigBlock loads the config the chart renders for the local
// overlay against the live CUE schema. Rendered through `helm template` rather
// than read out of values.yaml: the ConfigMap template adds keys of its own
// (the Postgres DSNs, the signing-key ref, the issuer), and the rendered file
// is what the pod reads.
//
// values-local.yaml is the one overlay holding concrete secrets that satisfy
// the schema's min-length / URL-format constraints; dev/staging/prod carry
// `<placeholder>` tokens that deploy-time Secrets replace and would fail
// Validate() here by design.
func TestHelmValuesConfigBlock(t *testing.T) {
	const (
		chartDir     = "../../deploy/chart"
		localOverlay = chartDir + "/values-local.yaml"
		configMapKey = "config.yaml"
	)
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Fatal("helm is not installed; the chart's rendered config went unchecked")
	}
	out, err := exec.CommandContext(t.Context(), helm, "template", "paladin-core", chartDir,
		"--namespace", "paladin", "-f", localOverlay,
		"--show-only", "templates/configmap.yaml").Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			t.Fatalf("helm template: %v\n%s", err, exitErr.Stderr)
		}
		t.Fatalf("helm template: %v", err)
	}
	var cm struct {
		Data map[string]string `yaml:"data"`
	}
	if err := goyaml.Unmarshal(out, &cm); err != nil {
		t.Fatalf("decode rendered ConfigMap: %v", err)
	}
	rendered, ok := cm.Data[configMapKey]
	if !ok {
		t.Fatalf("rendered ConfigMap has no %s key", configMapKey)
	}
	path := filepath.Join(t.TempDir(), configMapKey)
	if err := os.WriteFile(path, []byte(rendered), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load([]string{path}, zap.NewNop()); err != nil {
		t.Fatalf("Load the config the chart renders for values-local.yaml: %v", err)
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
	// ADR-0014 Phase 1: the canonical Collection EUID is enabled in the shipped
	// config. Guards against the flag silently reverting to the Go zero-value.
	if !cfg.Cedar.CanonicalCollectionEUID {
		t.Error("Cedar.CanonicalCollectionEUID: expected true from configs/config.yaml (ADR-0014 Phase 1)")
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

// TestShippedConfigDataPortMatchesSchema pins configs/config.yaml's data
// listener and MCP data upstream to the schema defaults. The file's header
// promises "defaults match schema.cue"; for the data port that promise is
// load-bearing, because a host-run stack on 8080 collides with another
// local service.
func TestShippedConfigDataPortMatchesSchema(t *testing.T) {
	shipped, err := Load([]string{"../../configs/config.yaml"}, zap.NewNop())
	if err != nil {
		t.Fatalf("Load configs/config.yaml: %v", err)
	}
	minimal := filepath.Join(t.TempDir(), "min.yaml")
	if err := os.WriteFile(minimal, []byte(minimalConfigYAML), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	defaults, err := Load([]string{minimal}, zap.NewNop())
	if err != nil {
		t.Fatalf("Load minimal yaml: %v", err)
	}
	if shipped.API.Server.Data.Addr != defaults.API.Server.Data.Addr {
		t.Errorf("api.server.data.addr: config.yaml %q, schema default %q",
			shipped.API.Server.Data.Addr, defaults.API.Server.Data.Addr)
	}
	if shipped.MCP.Upstreams.DataURL != defaults.MCP.Upstreams.DataURL {
		t.Errorf("mcp.upstreams.data_url: config.yaml %q, schema default %q",
			shipped.MCP.Upstreams.DataURL, defaults.MCP.Upstreams.DataURL)
	}
}
