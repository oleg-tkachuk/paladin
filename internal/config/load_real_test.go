package config

import (
	"testing"

	"go.uber.org/zap"
)

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
			if _, err := Load(path, zap.NewNop()); err != nil {
				t.Fatalf("Load %s: %v", path, err)
			}
		})
	}
	cfg, err := Load("../../configs/config.yaml", zap.NewNop())
	if err != nil {
		t.Fatalf("Load configs/config.yaml: %v", err)
	}
	if cfg.App.Name == "" {
		t.Error("App.Name is empty after load")
	}
	if cfg.Datastores.Postgres.DSN == "" {
		t.Error("Postgres.DSN is empty after load")
	}
	if !cfg.RateLimit.Enabled {
		t.Error("RateLimit.Enabled: expected true from yaml")
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
	if cfg.Server.DataHTTP.Addr == "" || cfg.Server.AdminHTTP.Addr == "" || cfg.Server.IAMHTTP.Addr == "" {
		t.Error("three-plane HTTP addrs incomplete")
	}
}
