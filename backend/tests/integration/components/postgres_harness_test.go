//go:build integration

package components

import (
	"context"
	"testing"
)

// Each startPostgres is its own database: what one test writes, another does
// not see, and both carry the full schema. The shared server must not turn
// into shared state.
func TestStartPostgresIsolatesEachCall(t *testing.T) {
	ctx := context.Background()
	a, b := startPostgres(t), startPostgres(t)

	var nameA, nameB string
	if err := a.QueryRow(ctx, `SELECT current_database()`).Scan(&nameA); err != nil {
		t.Fatal(err)
	}
	if err := b.QueryRow(ctx, `SELECT current_database()`).Scan(&nameB); err != nil {
		t.Fatal(err)
	}
	if nameA == nameB || nameA == templateDB || nameB == templateDB {
		t.Fatalf("databases %q and %q: want two distinct clones of %q", nameA, nameB, templateDB)
	}

	if _, err := a.Exec(ctx, `INSERT INTO tenants (slug, display_name) VALUES ('harness-a', 'A')`); err != nil {
		t.Fatalf("write in a: %v", err)
	}
	var inB int
	if err := b.QueryRow(ctx, `SELECT count(*) FROM tenants WHERE slug = 'harness-a'`).Scan(&inB); err != nil {
		t.Fatal(err)
	}
	if inB != 0 {
		t.Errorf("a row written in %s is visible in %s", nameA, nameB)
	}

	var verA, verB int64
	const latest = `SELECT max(version_id) FROM goose_db_version WHERE is_applied`
	if err := a.QueryRow(ctx, latest).Scan(&verA); err != nil {
		t.Fatal(err)
	}
	if err := b.QueryRow(ctx, latest).Scan(&verB); err != nil {
		t.Fatal(err)
	}
	if verA == 0 || verA != verB {
		t.Errorf("migration versions %d and %d: want the same, non-zero", verA, verB)
	}
}
