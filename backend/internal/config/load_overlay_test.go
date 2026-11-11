package config

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

// TestLoad_OverlayMerges confirms that paths[1..] override paths[0]
// at the leaf-key level (deep merge). The base file carries every
// required field; the overlay file just bumps app.env and the
// auth.session_key_path. Loaded result must reflect overlay values.
func TestLoad_OverlayMerges(t *testing.T) {
	dir := t.TempDir()

	base := filepath.Join(dir, "base.yaml")
	if err := os.WriteFile(base, []byte(`
app:
  name: paladin
  env: local
runtime:
  mode: debug
api:
  server:
    data: { addr: "0.0.0.0:8080" }
    iam:  { addr: "0.0.0.0:8085" }
admin:
  server: { addr: "0.0.0.0:8090" }
worker:
  ops: { addr: "0.0.0.0:8090" }
datastores:
  postgres:
    dsn: "postgres://x@y/z"
storage:
  backends:
    primary:
      kind: aws-s3
      auth: { mode: default_chain }
auth:
  session_key_path: /keys/base.pem
`), 0o600); err != nil {
		t.Fatalf("write base: %v", err)
	}

	overlay := filepath.Join(dir, "overlay.yaml")
	if err := os.WriteFile(overlay, []byte(`
app:
  env: prod
auth:
  session_key_path: /keys/overlay.pem
runtime:
  mode: release
`), 0o600); err != nil {
		t.Fatalf("write overlay: %v", err)
	}

	cfg, err := Load([]string{base, overlay}, zap.NewNop())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Overlay-set fields:
	if cfg.App.Env != "prod" {
		t.Errorf("app.env: got %q, want prod (overlay should win)", cfg.App.Env)
	}
	if cfg.Runtime.Mode != "release" {
		t.Errorf("runtime.mode: got %q, want release", cfg.Runtime.Mode)
	}
	if cfg.Auth.SessionKeyPath != "/keys/overlay.pem" {
		t.Errorf("auth.session_key_path: overlay should override; got %q", cfg.Auth.SessionKeyPath)
	}

	// Base-only fields unchanged by overlay:
	if cfg.App.Name != "paladin" {
		t.Errorf("app.name: base value lost; got %q", cfg.App.Name)
	}
	if cfg.Datastores.Postgres.DSN != "postgres://x@y/z" {
		t.Errorf("datastores.postgres.dsn: base value lost")
	}
}

// TestLoad_OverlayWithUnknownKey rejects an overlay that introduces a
// typo. Strict-key validation runs against the merged tree, not per-
// file, so the typo only fails when the merged set still doesn't
// match the schema.
func TestLoad_OverlayWithUnknownKey(t *testing.T) {
	dir := t.TempDir()

	base := filepath.Join(dir, "base.yaml")
	if err := os.WriteFile(base, []byte(`
app: { name: paladin, env: local }
runtime: { mode: debug }
datastores:
  postgres: { dsn: "postgres://x@y/z" }
storage:
  backends:
    primary:
      kind: aws-s3
      auth: { mode: default_chain }
`), 0o600); err != nil {
		t.Fatalf("write base: %v", err)
	}

	overlay := filepath.Join(dir, "overlay.yaml")
	if err := os.WriteFile(overlay, []byte(`
runtime:
  mod: debug   # typo — should be `+"`mode`"+`
`), 0o600); err != nil {
		t.Fatalf("write overlay: %v", err)
	}

	if _, err := Load([]string{base, overlay}, zap.NewNop()); err == nil {
		t.Fatalf("Load: want error on unknown key from overlay, got nil")
	}
}

// TestLoad_RejectsEmptyPaths surfaces operator misuse early instead
// of silently loading an empty config and tripping on the first
// missing field.
func TestLoad_RejectsEmptyPaths(t *testing.T) {
	if _, err := Load(nil, zap.NewNop()); err == nil {
		t.Fatalf("Load(nil): want error, got nil")
	}
	if _, err := Load([]string{}, zap.NewNop()); err == nil {
		t.Fatalf("Load([]): want error, got nil")
	}
}
