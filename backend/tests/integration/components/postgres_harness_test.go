//go:build integration

package components

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Each startPostgres is its own database: what one test writes, another does
// not see, and both carry the full schema. The shared server must not turn
// into shared state.
func TestStartPostgresIsolatesEachCall(t *testing.T) {
	t.Parallel()
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

// parallelClones is more tests than `go test` runs at once on most machines,
// so the clones below overlap the way a parallel package run does.
const parallelClones = 24

// Parallel tests clone the template at the same time and each hold pools
// open. Neither may fail for the other: not the clone ("source database is
// being accessed by other users"), not the connection limit.
func TestStartPostgresClonesInParallel(t *testing.T) {
	t.Parallel()
	for i := range parallelClones {
		t.Run(fmt.Sprintf("clone-%d", i), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			pool := startPostgres(t)
			admin := startPostgres(t)
			rls := rlsPool(t, ctx, admin)
			for _, p := range []*pgxpool.Pool{pool, admin, rls} {
				if err := p.Ping(ctx); err != nil {
					t.Fatalf("ping: %v", err)
				}
			}
		})
	}
}
