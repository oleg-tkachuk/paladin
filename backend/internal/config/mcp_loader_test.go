package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTempYAML(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write tmp yaml: %v", err)
	}
	return path
}

func TestLoadMCPDefaults(t *testing.T) {
	cfg, err := LoadMCP("")
	if err != nil {
		t.Fatalf("LoadMCP empty: %v", err)
	}
	if !cfg.Stdio.Enabled || !cfg.HTTP.Enabled {
		t.Errorf("expected stdio+http enabled by default, got %+v", cfg)
	}
	if cfg.HTTP.Addr != ":8095" {
		t.Errorf("HTTP.Addr default: got %q", cfg.HTTP.Addr)
	}
	if cfg.HTTP.SessionTimeout != 10*time.Minute {
		t.Errorf("HTTP.SessionTimeout default: got %v", cfg.HTTP.SessionTimeout)
	}
}

func TestLoadMCPYAMLOverrides(t *testing.T) {
	path := writeTempYAML(t, `
mcp:
  upstreams:
    admin_url: "https://admin.example.com"
    tls:
      ca_path: /etc/paladin-mtls/ca.crt
  stdio:
    enabled: false
  http:
    enabled: true
    addr: ":9000"
    profile: agent_safe
    session_timeout: 5m
`)
	cfg, err := LoadMCP(path)
	if err != nil {
		t.Fatalf("LoadMCP: %v", err)
	}
	if cfg.Stdio.Enabled {
		t.Error("stdio should be disabled by yaml")
	}
	if cfg.Upstreams.TLS.CaPath != "/etc/paladin-mtls/ca.crt" {
		t.Errorf("upstream tls ca_path = %q, want the yaml value", cfg.Upstreams.TLS.CaPath)
	}
	if cfg.HTTP.Addr != ":9000" {
		t.Errorf("addr: got %q want :9000", cfg.HTTP.Addr)
	}
	if cfg.HTTP.Profile != "agent_safe" {
		t.Errorf("http profile: got %q want agent_safe", cfg.HTTP.Profile)
	}
	if cfg.HTTP.SessionTimeout != 5*time.Minute {
		t.Errorf("session_timeout: got %v want 5m", cfg.HTTP.SessionTimeout)
	}
	if cfg.Upstreams.AdminURL != "https://admin.example.com" {
		t.Errorf("admin url override: got %q", cfg.Upstreams.AdminURL)
	}
	// Untouched fields should keep defaults.
	if cfg.Upstreams.DataURL != "http://localhost:8080" {
		t.Errorf("data url default leaked: got %q", cfg.Upstreams.DataURL)
	}
}

func TestLoadMCPEnvOverridesYAML(t *testing.T) {
	path := writeTempYAML(t, `
mcp:
  http:
    enabled: true
    addr: ":9000"
`)
	t.Setenv("PALADIN_MCP_HTTP_ENABLED", "false")
	t.Setenv("PALADIN_MCP_HTTP_ADDR", ":7777")
	cfg, err := LoadMCP(path)
	if err != nil {
		t.Fatalf("LoadMCP: %v", err)
	}
	if cfg.HTTP.Enabled {
		t.Error("env should override yaml: enabled=false expected")
	}
	if cfg.HTTP.Addr != ":7777" {
		t.Errorf("addr: got %q want :7777", cfg.HTTP.Addr)
	}
}

func TestLoadMCPMissingFileErrors(t *testing.T) {
	if _, err := LoadMCP("/no/such/path/config.yaml"); err == nil {
		t.Error("expected error for missing config path")
	}
}
