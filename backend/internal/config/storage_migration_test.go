package config

import (
	"testing"
	"time"
)

// The longest presign TTL the tests configure, below the schema's default
// max_ttl so a default that ignored it would show, and as the config spells
// it.
const (
	shortMaxTTL     = 24 * time.Hour
	shortMaxTTLText = "24h"
)

// A completed storage migration keeps the source copies, unless the operator
// says otherwise, for as long as a GET URL can live: max_ttl, whatever the
// deployment set it to.
func TestSourceCopyRetentionFollowsMaxTTL(t *testing.T) {
	for name, tc := range map[string]struct {
		extra string
		want  time.Duration
	}{
		"nothing set":   {"", SigV4MaxPresignExpiry},
		"max_ttl set":   {"limits:\n  presign:\n    max_ttl: " + shortMaxTTLText + "\n", shortMaxTTL},
		"retention set": {"worker:\n  jobs:\n    storage_migration:\n      source_copy_retention: 1h\n", time.Hour},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := loadTrashRetention(t, tc.extra)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got := cfg.Worker.Jobs.StorageMigration.SourceCopyRetention; got != tc.want {
				t.Fatalf("worker.jobs.storage_migration.source_copy_retention = %v, want %v", got, tc.want)
			}
		})
	}
}

// A promoted object's anchor settles on its own grace, not min_object_age.
func TestWriteSettleGraceDefault(t *testing.T) {
	cfg, err := loadTrashRetention(t, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	const want = 5 * time.Minute
	if got := cfg.Worker.Jobs.Reconciler.WriteSettleGrace; got != want {
		t.Fatalf("worker.jobs.reconciler.write_settle_grace = %v, want %v", got, want)
	}
	if cfg.Worker.Jobs.Reconciler.WriteSettleGrace == cfg.Worker.Jobs.Reconciler.MinObjectAge {
		t.Fatal("write_settle_grace defaults to min_object_age; they are separate graces")
	}
}
