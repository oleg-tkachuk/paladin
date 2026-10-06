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

// Keys removed because nothing read them. Each looked like a working control:
// security.reject_tenant_mismatch (true everywhere) once gated a REST
// middleware that went with the move to Connect; housekeeping.pending_ttl
// claimed to expire PENDING objects, which expire with their presigned URL;
// housekeeping.delete_orphaned_parts warned to enable it in prod to reclaim
// storage the multipart reaper already reclaims. A config still carrying one
// must fail to load rather than keep implying the knob exists.
func TestValidateNoUnknownKeys_RemovedDeadKeysFail(t *testing.T) {
	cases := map[string]string{
		"security.reject_tenant_mismatch":                "security:\n  reject_tenant_mismatch: true\n",
		"worker.jobs.housekeeping.pending_ttl":           "worker:\n  jobs:\n    housekeeping:\n      pending_ttl: 24h\n",
		"worker.jobs.housekeeping.delete_orphaned_parts": "worker:\n  jobs:\n    housekeeping:\n      delete_orphaned_parts: true\n",
	}
	for key, body := range cases {
		t.Run(key, func(t *testing.T) {
			err := validateNoUnknownKeys(writeYAML(t, body))
			if err == nil {
				t.Fatalf("expected the removed %s to be refused", key)
			}
			if !strings.Contains(err.Error(), key) {
				t.Errorf("error must name the removed key, got: %v", err)
			}
		})
	}
}
