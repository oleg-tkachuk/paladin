package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeYAML(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	return path
}

func TestValidateNoUnknownKeys_KnownYAMLPasses(t *testing.T) {
	path := writeYAML(t, `
app:
  name: paladin
  env: local
auth:
  signing_key: "x"
storage:
  backends:
    primary:
      kind: aws-s3
      auth:
        mode: static_keys
`)
	if err := validateNoUnknownKeys(path); err != nil {
		t.Fatalf("known-only YAML should pass: %v", err)
	}
}

func TestValidateNoUnknownKeys_TopLevelTypoFails(t *testing.T) {
	path := writeYAML(t, `
app:
  name: paladin
  env: local
authorization:           # typo for "auth"
  signing_key: "x"
`)
	err := validateNoUnknownKeys(path)
	if err == nil {
		t.Fatal("expected error for top-level typo")
	}
	if !strings.Contains(err.Error(), "authorization") {
		t.Errorf("error must name the offending key, got: %v", err)
	}
}

func TestValidateNoUnknownKeys_NestedTypoFails(t *testing.T) {
	path := writeYAML(t, `
auth:
  signing_key: "x"
  singing_key: "y"        # typo for signing_key
`)
	err := validateNoUnknownKeys(path)
	if err == nil {
		t.Fatal("expected error for nested typo")
	}
	if !strings.Contains(err.Error(), "auth.singing_key") {
		t.Errorf("error must include nested path, got: %v", err)
	}
}

func TestValidateNoUnknownKeys_StorageBackendsAreOpenMap(t *testing.T) {
	// storage.backends.<name> is a map — arbitrary backend names must
	// pass, but unknown fields under one of them must still fail.
	path := writeYAML(t, `
storage:
  backends:
    primary:
      kind: aws-s3
    weird-name-with-dashes:
      kind: aws-s3
    typo-here:
      kind: aws-s3
      kynd: aws-s3       # typo for "kind"
`)
	err := validateNoUnknownKeys(path)
	if err == nil {
		t.Fatal("expected error for typo inside a map-keyed backend")
	}
	if !strings.Contains(err.Error(), "kynd") {
		t.Errorf("error must surface the typo: %v", err)
	}
}

func TestValidateNoUnknownKeys_MultipleErrorsListed(t *testing.T) {
	path := writeYAML(t, `
app:
  name: paladin
  ennv: local             # typo
loger:                    # typo
  level: debug
`)
	err := validateNoUnknownKeys(path)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "app.ennv") || !strings.Contains(msg, "loger") {
		t.Errorf("both typos should surface; got: %v", err)
	}
}

func TestValidateNoUnknownKeys_MissingFileIsLenient(t *testing.T) {
	// validateNoUnknownKeys treats file-read errors as "not our problem"
	// — the main loader surfaces those. This avoids double-error-noise.
	if err := validateNoUnknownKeys("/no/such/path"); err != nil {
		t.Errorf("missing file should not produce a strict-key error: %v", err)
	}
}

func TestValidateNoUnknownKeys_RealConfigPasses(t *testing.T) {
	// The canonical example must round-trip cleanly.
	if err := validateNoUnknownKeys("../../configs/config.yaml"); err != nil {
		t.Errorf("configs/config.yaml has unknown keys?\n%v", err)
	}
	if err := validateNoUnknownKeys("../../configs/local.yaml"); err != nil {
		t.Errorf("configs/local.yaml has unknown keys?\n%v", err)
	}
}
