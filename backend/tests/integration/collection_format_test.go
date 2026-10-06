//go:build integration

// Pins the collection_format CHECK after the schema baseline (001_initial_schema.sql) fixed the
// exactly-2-char-segment rejection. Pure DB-constraint behaviour, so it can
// only be verified against the real schema.
package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func insertCollection(pool *pgxpool.Pool, tenant uuid.UUID, ok string) error {
	_, err := pool.Exec(context.Background(),
		`INSERT INTO collections (tenant_id, name, bucket_id)
		 SELECT $1, $2, b.id
		   FROM buckets b
		   JOIN storage_backends sb ON sb.id = b.backend_id
		  WHERE sb.name = 'primary' AND b.name = 'paladin-test'`,
		tenant, ok)
	return err
}

func TestCollectionFormat_SegmentLengths(t *testing.T) {
	f := setupDispatcher(t)
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "okfmt")
	// Seeds the 'primary' backend + 'paladin-test' bucket (FK targets) plus one OK.
	mustCreateCollection(t, f.h.PoolMigrate, tenant, "seed")

	seg63 := "a" + strings.Repeat("b", 61) + "c" // 63 chars: max per-segment

	valid := []string{
		"a",                  // 1-char segment
		"eu",                 // 2-char segment — once rejected by the DB
		"qa",                 // 2-char
		"us-west",            // hyphenated
		"abc",                // 3-char
		seg63,                // 63-char (max)
		"invoices/eu/report", // multi-segment incl a 2-char segment
		"a/bb/ccc",           // mixed 1/2/3-char segments
	}
	for _, ok := range valid {
		if err := insertCollection(f.h.PoolMigrate, tenant, ok); err != nil {
			t.Errorf("collection %q should be ACCEPTED; got %v", ok, err)
		}
	}

	invalid := []string{
		"",          // empty
		"-bad",      // leading hyphen
		"bad-",      // trailing hyphen
		"UPPER",     // uppercase
		"a/",        // trailing slash (empty final segment)
		"a//b",      // empty middle segment
		seg63 + "d", // 64-char segment exceeds the per-segment max
	}
	for _, ok := range invalid {
		if err := insertCollection(f.h.PoolMigrate, tenant, ok); err == nil {
			t.Errorf("collection %q should be REJECTED, but it was accepted", ok)
		}
	}
}
