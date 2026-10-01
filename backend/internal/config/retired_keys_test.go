package config

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// security.log_sensitive was declared, defaulted and shipped in every config,
// and read by nothing: no secret was ever logged on its account. It is still
// accepted, so an existing config keeps loading, but setting it to true is a
// promise the binary does not keep, and the load says so.
func TestLoad_WarnsOnRetiredLogSensitive(t *testing.T) {
	for name, tc := range map[string]struct {
		extra string
		warn  bool
	}{
		"set true":  {"security:\n  log_sensitive: true\n", true},
		"set false": {"security:\n  log_sensitive: false\n", false},
		"absent":    {"", false},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "c.yaml")
			body := `
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
` + tc.extra
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			core, logs := observer.New(zap.WarnLevel)
			if _, err := Load([]string{path}, zap.New(core)); err != nil {
				t.Fatalf("Load: %v", err)
			}
			got := logs.FilterMessage(retiredLogSensitiveWarning).Len() > 0
			if got != tc.warn {
				t.Errorf("warned = %v, want %v", got, tc.warn)
			}
		})
	}
}
