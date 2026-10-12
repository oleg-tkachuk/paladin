package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

const trashRetentionBase = `
app:
  name: paladin
  env: local
datastores:
  postgres:
    dsn: "postgres://x@y/z"
storage:
  backends:
    primary:
      kind: aws-s3
      auth: { mode: default_chain }
auth:
  signing_key: "base-key-must-be-32-bytes-long-or-more"
`

func loadTrashRetention(t *testing.T, extra string) (Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(trashRetentionBase+extra), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load([]string{path}, zap.NewNop())
}

// An operator who says nothing gets the owner's default: 30 days.
func TestTrashRetentionDefaultsToThirtyDays(t *testing.T) {
	cfg, err := loadTrashRetention(t, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := cfg.Admin.Tenants.TrashRetention, 30*24*time.Hour; got != want {
		t.Fatalf("admin.tenants.trash_retention = %v, want %v", got, want)
	}
}

func TestTrashRetentionIsConfigurable(t *testing.T) {
	cfg, err := loadTrashRetention(t, "admin:\n  tenants:\n    trash_retention: 168h\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Admin.Tenants.TrashRetention; got != 168*time.Hour {
		t.Fatalf("admin.tenants.trash_retention = %v, want 168h", got)
	}
}

// A zero window would surface every tenant for purge the moment it was
// trashed; the loader refuses it.
func TestTrashRetentionMustBePositive(t *testing.T) {
	_, err := loadTrashRetention(t, "admin:\n  tenants:\n    trash_retention: 0s\n")
	if err == nil || !strings.Contains(err.Error(), "admin.tenants.trash_retention") {
		t.Fatalf("Load with trash_retention: 0s: err = %v, want it refused", err)
	}

	c := minimalValidConfig()
	c.Admin.Tenants.TrashRetention = -time.Hour
	mustReject(t, c, "admin.tenants.trash_retention")
}
